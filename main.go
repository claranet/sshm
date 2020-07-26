package main

import (
	"bufio"
	"encoding/json"
	"flag"
	"log"
	"os"
	"regexp"
	"sort"
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
	InstanceID       string
	ComputerName     string
	PrivateIPAddress string
	PublicIPAddress  string
	Name             string
	InstanceState    string
	AgentState       string
	PlatformType     string
	PlatformName     string
}

var allInstances []instance
var managedInstances []instance

func main() {
	profile := flag.String("p", "", "Profile from ~/.aws/config")
	region := flag.String("r", "", "Region, default is eu-west-1")
	instance := flag.String("i", "", "InstanceID for direct connection")
	portNumber := flag.String("pn", "", "Port Number for Proxy")
	localPortNumber := flag.String("lpn", "", "Local Port Number for Proxy")
	flag.Parse()

	source := flag.Arg(0)
	destination := flag.Arg(1)

	if *profile == "" {
		if os.Getenv("AWS_PROFILE") != "" {
			*profile = os.Getenv("AWS_PROFILE")
		} else if os.Getenv("AWS_DEFAULT_PROFILE") != "" {
			*profile = os.Getenv("AWS_DEFAULT_PROFILE")
		} else {
			p := listProfiles()
			sort.Strings(p)
			*profile = selectProfile(p)
		}
	}
	if *region == "" {
		if os.Getenv("AWS_REGION") != "" {
			*region = os.Getenv("AWS_PROFILE")
		} else if os.Getenv("AWS_DEFAULT_REGION") != "" {
			*region = os.Getenv("AWS_DEFAULT_PROFILE")
		} else {
			*region = "eu-west-1"
		}
	}

	// Create session (credentials from ~/.aws/config)
	sess := session.Must(session.NewSessionWithOptions(session.Options{
		SharedConfigState:       session.SharedConfigEnable,  //enable use of ~/.aws/config
		AssumeRoleTokenProvider: stscreds.StdinTokenProvider, //ask for MFA if needed
		Profile:                 string(*profile),
		Config:                  aws.Config{Region: aws.String(*region)},
	}))

	if *instance != "" {
		startSSH(*instance, region, profile, portNumber, localPortNumber, source, destination, sess)
	} else {
		allInstances = listAllInstances(sess)
		managedInstances = listManagedInstances(sess)
		if len(managedInstances) == 0 {
			log.Fatal("No available instance")
		}
		if selected := selectInstance(managedInstances); selected != "" {
			startSSH(selected, region, profile, portNumber, localPortNumber, source, destination, sess)
		}
	}
}

func listProfiles() []string {
	var profiles []string
	file, err := os.Open(os.Getenv("HOME") + "/.aws/config")
	if err != nil {
		log.Fatal(err)
	}
	defer file.Close()

	reg := regexp.MustCompile(`^\[profile `)

	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		if t := reg.MatchString(scanner.Text()); t == true {
			s := strings.TrimSuffix(reg.ReplaceAllString(scanner.Text(), "${1}"), "]")
			profiles = append(profiles, s)
		}
	}

	if err := scanner.Err(); err != nil {
		log.Fatal(err)
	}

	return profiles
}

func selectProfile(profiles []string) string {
	templates := &promptui.SelectTemplates{
		// Label:    ``,
		Active:   `{{ "> " | cyan | bold }}{{ . | cyan | bold }}`,
		Inactive: `  {{ . }}`,
	}

	searcher := func(input string, index int) bool {
		j := profiles[index]
		name := strings.ToLower(j)
		input = strings.ToLower(input)

		return strings.Contains(name, input)
	}

	prompt := promptui.Select{
		Label:             "Profile",
		Items:             profiles,
		Templates:         templates,
		Size:              10,
		Searcher:          searcher,
		StartInSearchMode: true,
	}

	selected, _, err := prompt.Run()
	if err != nil {
		os.Exit(0)
	}

	return profiles[selected]
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
			e.InstanceID = *i.InstanceId
			e.InstanceState = *i.State.Name
			e.PublicIPAddress = "None"
			if i.PublicIpAddress != nil {
				e.PublicIPAddress = *i.PublicIpAddress
			} else {
				e.PublicIPAddress = "N/A"
			}
			switch *i.State.Name {
			case "terminated", "shutting-down":
			//
			default:
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
			e.InstanceID = *i.InstanceId
			e.AgentState = *i.PingStatus
			if *i.PingStatus == "Online" {
				e.ComputerName = *i.ComputerName
				e.PrivateIPAddress = *i.IPAddress
				e.PlatformType = *i.PlatformType
				e.PlatformName = *i.PlatformName + " " + *i.PlatformVersion
			}
			for _, j := range allInstances {
				if *i.InstanceId == j.InstanceID {
					e.Name = j.Name
					e.PublicIPAddress = j.PublicIPAddress
					e.InstanceState = j.InstanceState
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

	formattedInstancesList := getFormattedInstancesList(managedInstances)

	templates := &promptui.SelectTemplates{
		// Label:    ``,
		Active:   `{{ if eq .AgentState "Online" }}{{ "> " | cyan | bold }}{{ .Name | cyan | bold }}{{ " | " | cyan | bold }}{{ .ComputerName | cyan | bold }}{{ " | " | cyan | bold }}{{ .InstanceID | cyan | bold }}{{ " | " | cyan | bold }}{{ .PrivateIPAddress | cyan | bold }}{{ else }}{{ "> " | red | bold }}{{ .Name | red | bold }}{{ " | " | red | bold }}{{ .ComputerName | red | bold }}{{ " | " | red | bold }}{{ .InstanceID | red | bold }}{{ " | " | red | bold }}{{ .PrivateIPAddress | red | bold }}{{ end }}`,
		Inactive: `  {{ if eq .AgentState "Online" }}{{ .Name }}{{ " | " }}{{ .ComputerName }}{{ " | " }}{{ .InstanceID }}{{ " | " }}{{ .PrivateIPAddress }}{{ else }}{{ .Name | red }}{{ " | "  | red }}{{ .ComputerName | red }}{{ " | " | red}}{{ .InstanceID | red }}{{ " | " | red }}{{ .PrivateIPAddress | red }}{{ end }}`,
		Details: `
{{ "PublicIP: " }}{{ .PublicIPAddress }}{{ " | PlatformType: " }}{{ .PlatformType }}{{ " | PlatformName: " }}{{ .PlatformName }}{{ " | Agent: "}}{{ if eq .AgentState "Online" }}{{ .AgentState | bgGreen }}{{ else }}{{ .AgentState | bgRed }}{{ end }}{{ " | State: "}}{{ .InstanceState }}`,
	}

	searcher := func(input string, index int) bool {
		j := managedInstances[index]
		name := strings.Replace(strings.ToLower(j.InstanceID+j.ComputerName+j.PrivateIPAddress+j.PublicIPAddress+j.Name+j.InstanceState+j.AgentState+j.PlatformType+j.PlatformName), " ", "", -1)
		input = strings.Replace(strings.ToLower(input), " ", "", -1)

		return strings.Contains(name, input)
	}

	var countRunning, countOnline int
	for _, i := range allInstances {
		if i.InstanceState == "running" {
			countRunning++
		}
	}
	for _, i := range managedInstances {
		if i.AgentState == "Online" {
			countOnline++
		}
	}

	prompt := promptui.Select{
		Label:             "Online: " + strconv.Itoa(countOnline) + " | Offline: " + strconv.Itoa(len(managedInstances)-countOnline) + " | Running: " + strconv.Itoa(countRunning) + " ",
		Items:             formattedInstancesList,
		Templates:         templates,
		Size:              15,
		Searcher:          searcher,
		StartInSearchMode: true,
		HideSelected:      true,
		// HideHelp:          true,
	}

	selected, _, err := prompt.Run()
	if err != nil {
		return ""
	}

	return managedInstances[selected].InstanceID
}

func startSSH(instanceID string, region, profile, portNumber, localPortNumber *string, source, destination string, sess *session.Session) {
	client := ssm.New(sess)
	input := &ssm.StartSessionInput{Target: aws.String(instanceID)}
	if *portNumber != "" && *localPortNumber != "" && source == "" {
		input.DocumentName = aws.String("AWS-StartPortForwardingSession")
		input.Parameters = map[string][]*string{"portNumber": []*string{aws.String(*portNumber)}, "localPortNumber": []*string{aws.String(*localPortNumber)}}
	}

	ssmSess, err := client.StartSession(input)
	if err != nil {
		log.Fatal(err.Error())
	}
	payloadJSON, _ := json.Marshal(ssmSess)
	inputJSON, _ := json.Marshal(input)

	cmd := exec.Command("session-manager-plugin", string(payloadJSON), *region, "StartSession", *profile, string(inputJSON))
	cmd.Stdout = os.Stdout
	cmd.Stdin = os.Stdin
	cmd.Stderr = os.Stderr
	signal.Ignore(syscall.SIGINT)
	cmd.Run()
}

func getFormattedInstancesList(managedInstances []instance) []instance {
	var size1, size2, size3, size4 int
	for _, i := range managedInstances {
		if len(i.Name) > size1 {
			size1 = len(i.Name)
		}
		if len(i.ComputerName) > size2 {
			size2 = len(i.ComputerName)
		}
		if len(i.InstanceID) > size3 {
			size3 = len(i.InstanceID)
		}
		if len(i.PrivateIPAddress) > size4 {
			size4 = len(i.PrivateIPAddress)
		}
	}

	var formattedInstancesList []instance
	for _, i := range managedInstances {
		var fi instance
		fi.Name = addSpaces(i.Name, size1)
		fi.ComputerName = addSpaces(i.ComputerName, size2)
		fi.InstanceID = addSpaces(i.InstanceID, size3)
		fi.PrivateIPAddress = addSpaces(i.PrivateIPAddress, size4)
		fi.PublicIPAddress = i.PublicIPAddress
		fi.InstanceState = i.InstanceState
		fi.AgentState = i.AgentState
		fi.PlatformType = i.PlatformType
		fi.PlatformName = i.PlatformName
		formattedInstancesList = append(formattedInstancesList, fi)
	}
	return formattedInstancesList
}

func addSpaces(text string, size int) string {
	for i := 0; size-len(text) > 0; i++ {
		text += " "
	}
	return text
}
