# SSHM

## Description

Easy connect on EC2 instances thanks to AWS System Manager.

## Usage

```
Usage of sshm:
    -profile string
        Profile from ~/.aws/config (default "default")
    -region string
        Region (default "eu-west-1")
```
You can select your instance by &larr;, &uarr;, &rarr; &darr; and filter by typing. **Enter** to validate.

## Output Example

![screenshot1](./img/screenshot1.png)
![screenshot2](./img/screenshot2.png)

## Build

```
go build
```
This repository uses `go mod`, so don't `git clone` inside your `$GOPATH`.

## Author

Thomas Labarussias (thomas.labarussias@fr.clara.net)