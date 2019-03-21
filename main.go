package main

import (
	"encoding/json"
	"flag"
	"log"
	"os"
	"strconv"
	"strings"
	"syscall"

	"os/exec"
	"os/signal"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/aws/credentials/stscreds"
	"github.com/aws/aws-sdk-go/aws/session"
	"github.com/aws/aws-sdk-go/service/ec2"
	"github.com/aws/aws-sdk-go/service/ssm"
	"github.com/manifoldco/promptui"
)

type instance struct {
	InstanceId       string
	ComputerName     string
	PrivateIpAddress string
	PublicIpAddress  string
	Name             string
	State            string
	Platform         string
}

var allInstances []instance
var managedInstances []instance

func main() {
	profile := flag.String("profile", "default", "Profile from ~/.aws/config")
	region := flag.String("region (only to create session)", "eu-west-1", "Region")
	flag.Parse()

	// Create session (credentials from ~/.aws/config)
	sess := session.Must(session.NewSessionWithOptions(session.Options{
		SharedConfigState:       session.SharedConfigEnable,  //enable use of ~/.aws/config
		AssumeRoleTokenProvider: stscreds.StdinTokenProvider, //ask for MFA if needed
		Profile:                 string(*profile),
		Config:                  aws.Config{Region: aws.String(*region)},
	}))

	allInstances = listAllInstances(sess)
	managedInstances = listManagedInstances(sess)
	if len(managedInstances) == 0 {
		log.Fatal("No available instance")
	}

	if selected := selectInstance(managedInstances); selected != "" {
		startSSH(selected, region, profile, sess)
	}
}

func listAllInstances(sess *session.Session) []instance {
	client := ec2.New(sess)
	input := &ec2.DescribeInstancesInput{}
	response, err := client.DescribeInstances(input)
	if err != nil {
		log.Fatal(err.Error())
	}

	var instances []instance
	for _, reservation := range response.Reservations {
		for _, i := range reservation.Instances {
			var e instance
			e.Name = "unnamed"
			for _, tag := range i.Tags {
				if *tag.Key == "Name" {
					e.Name = *tag.Value
				}
			}
			e.InstanceId = *i.InstanceId
			e.State = *i.State.Name
			e.PublicIpAddress = "None"
			if i.PublicIpAddress != nil {
				e.PublicIpAddress = *i.PublicIpAddress
			}
			if *i.State.Name == "running" {
				instances = append(instances, e)
			}
		}
	}
	return instances
}

func listManagedInstances(sess *session.Session) []instance {
	client := ssm.New(sess)
	input := &ssm.DescribeInstanceInformationInput{MaxResults: aws.Int64(50)}
	var instances []instance
	for {
		info, err := client.DescribeInstanceInformation(input)
		if err != nil {
			log.Println(err.Error())
		}
		for _, i := range info.InstanceInformationList {
			var e instance
			e.InstanceId = *i.InstanceId
			e.ComputerName = *i.ComputerName
			e.PrivateIpAddress = *i.IPAddress
			e.Platform = *i.PlatformType
			e.State = *i.PingStatus
			for _, j := range allInstances {
				if *i.InstanceId == j.InstanceId {
					e.Name = j.Name
					e.PublicIpAddress = j.PublicIpAddress
				}
			}
			instances = append(instances, e)
		}
		if info.NextToken == nil {
			break
		}
		input.SetNextToken(*info.NextToken)
	}
	return instances
}

func selectInstance(managedInstances []instance) string {
	templates := &promptui.SelectTemplates{
		// Label:    ``,
		Active:   `{{ "> " | cyan | bold }}{{ .Name | cyan | bold }}{{ " - " | cyan | bold }}{{ .ComputerName | cyan | bold }}{{ " - " | cyan | bold }}{{ .InstanceId | cyan | bold }}{{ " - " | cyan | bold }}{{ .PrivateIpAddress | cyan | bold }}`,
		Inactive: `  {{ .Name }}{{ " - " }}{{ .ComputerName }}{{ " - " }}{{ .InstanceId }}{{ " - " }}{{ .PrivateIpAddress }}`,
		Details: `
{{ "PublicIP: " }}{{ .PublicIpAddress }}{{ " | Platform: " }}{{ .Platform }}{{ " | State: "}}{{ .State }}`,
	}

	searcher := func(input string, index int) bool {
		j := managedInstances[index]
		name := strings.Replace(strings.ToLower(j.Name+j.ComputerName+j.InstanceId+j.PrivateIpAddress), " ", "", -1)
		input = strings.Replace(strings.ToLower(input), " ", "", -1)

		return strings.Contains(name, input)
	}

	prompt := promptui.Select{
		Label:             strconv.Itoa(len(managedInstances)) + "/" + strconv.Itoa(len(allInstances)) + " instances",
		Items:             managedInstances,
		Templates:         templates,
		Size:              10,
		Searcher:          searcher,
		StartInSearchMode: true,
		// HideSelected:      true,
		// HideHelp:          true,
	}

	selected, _, err := prompt.Run()
	if err != nil {
		return ""
	}

	return managedInstances[selected].InstanceId
}

func startSSH(instanceId string, region, profile *string, sess *session.Session) {
	client := ssm.New(sess)
	input := &ssm.StartSessionInput{Target: aws.String(instanceId)}

	ssmSess, err := client.StartSession(input)
	if err != nil {
		log.Fatal(err.Error())
	}
	json, _ := json.Marshal(ssmSess)

	cmd := exec.Command("session-manager-plugin", string(json), *region, "StartSession", *profile)
	cmd.Stdout = os.Stdout
	cmd.Stdin = os.Stdin
	cmd.Stderr = os.Stderr
	signal.Ignore(syscall.SIGINT)
	cmd.Run()
}
