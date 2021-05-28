package main

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io/ioutil"
	"net"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"strings"

	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	"github.com/aws/aws-sdk-go-v2/service/ec2/types"
	"github.com/aws/aws-sdk-go-v2/service/ec2instanceconnect"
)

type instanceInfo struct {
	username  string
	ipAddress string
	host      string
}

var regions = []string{"us-west-1", "us-west-2"}

func ssh(ctx context.Context, args []string) error {
	options, err := sshOptions(ctx, args)
	if err != nil {
		return err
	}

	instance, err := instanceInfoFromString(options["hostname"][0], options["user"][0])
	if err != nil {
		return err
	}

	pk, err := existingKey(options["identityfile"])
	if err != nil {
		return err
	}

	publicKey, err := getPublicKey(pk)
	if err != nil {
		return fmt.Errorf("cannot read the public key %s.pub. If you want to provide a custom key location, use the `-i` parameter", pk)
	}

	for _, region := range regions {
		found, err := setupEC2Instance(ctx, instance, publicKey, region)
		if err != nil {
			return err
		}

		if found {
			break
		}
	}

	return connectToInstance(ctx, args)
}

func sshOptions(ctx context.Context, args []string) (map[string][]string, error) {
	args = append([]string{"-G"}, args...)
	cmd := exec.CommandContext(ctx, "ssh", args...)

	s := ""
	buff := bytes.NewBufferString(s)
	cmd.Stdout = buff
	cmd.Stderr = os.Stdout
	cmd.Stdin = os.Stdin

	if err := cmd.Run(); err != nil {
		return nil, err
	}
	res := map[string][]string{}

	scanner := bufio.NewScanner(buff)
	for scanner.Scan() {
		parts := strings.Split(scanner.Text(), " ")
		if len(parts) < 1 {
			continue
		}

		if _, exists := res[parts[0]]; !exists {
			res[parts[0]] = []string{}
			continue
		}

		res[parts[0]] = append(res[parts[0]], strings.Join(parts[1:], " "))
	}

	return res, nil
}

func instanceInfoFromString(hostname, user string) (*instanceInfo, error) {
	info := &instanceInfo{
		username: user,
		host:     hostname,
	}

	err := info.resolveIP()
	if err != nil {
		return nil, err
	}
	return info, nil
}

func (info *instanceInfo) resolveIP() error {
	resolver := net.Resolver{}
	ips, err := resolver.LookupIP(context.Background(), "ip", info.host)
	if err != nil {
		return err
	}

	for _, ip := range ips {
		info.ipAddress = ip.String()
		break
	}

	return nil
}

func existingKey(paths []string) (string, error) {
	for _, path := range paths {
		path, err := expandHomeDirectoryTilde(path)
		if err != nil {
			return "", err
		}

		if _, err := os.Stat(path); errors.Is(err, os.ErrNotExist) {
			continue
		}

		return path, nil
	}

	return "", errors.New("cannot find any ssh key")
}

// expandHomeDirectoryTilde expands the `~` to path to user's home directory.
// More info here: https://stackoverflow.com/questions/47261719/how-can-i-resolve-a-relative-path-to-absolute-path-in-golang
func expandHomeDirectoryTilde(path string) (string, error) {
	if len(path) == 0 || path[0] != '~' {
		return path, nil
	}

	usr, err := user.Current()
	if err != nil {
		return "", err
	}
	return filepath.Join(usr.HomeDir, path[1:]), nil
}

func readFile(path string) (string, error) {
	content, err := ioutil.ReadFile(path)
	if err != nil {
		return "", err
	}

	s := string(content)
	return s, nil
}

func getPublicKey(private string) (string, error) {
	return readFile(private + ".pub")
}

func setupEC2Instance(ctx context.Context, instance *instanceInfo, publicKey, region string) (bool, error) {
	cfg, err := config.LoadDefaultConfig(ctx, config.WithRegion(region))
	if err != nil {
		return false, fmt.Errorf("cannot get config for AWS: %w", err)
	}

	client := ec2.NewFromConfig(cfg)

	ec2Instance, err := findEC2Instance(ctx, client, instance)
	if err != nil {
		return false, err
	}

	if ec2Instance == nil {
		return false, nil
	}

	status, err := instanceStatus(ctx, client, *ec2Instance)
	if err != nil {
		return false, fmt.Errorf("cannot get the instance status: %w", err)
	}

	connect := ec2instanceconnect.NewFromConfig(cfg)
	out, err := connect.SendSSHPublicKey(ctx, &ec2instanceconnect.SendSSHPublicKeyInput{
		AvailabilityZone: status.AvailabilityZone,
		InstanceId:       ec2Instance.InstanceId,
		InstanceOSUser:   &instance.username,
		SSHPublicKey:     &publicKey,
	})

	if err != nil {
		return false, fmt.Errorf("cannot upload the public key: %w", err)
	}

	if !out.Success {
		return false, fmt.Errorf("unsuccessful uploaded the public key")
	}

	return true, nil
}

func instanceStatus(ctx context.Context, client *ec2.Client, instance types.Instance) (types.InstanceStatus, error) {
	descResp, err := client.DescribeInstanceStatus(ctx, &ec2.DescribeInstanceStatusInput{
		InstanceIds: []string{*instance.InstanceId},
	})

	if err != nil {
		return types.InstanceStatus{}, err
	}

	status := descResp.InstanceStatuses[0]
	return status, nil
}

func findEC2Instance(ctx context.Context, client *ec2.Client, info *instanceInfo) (*types.Instance, error) {
	resp, err := client.DescribeInstances(ctx, &ec2.DescribeInstancesInput{
		Filters: []types.Filter{
			{
				Name:   strp("private-ip-address"),
				Values: []string{info.ipAddress},
			},
		},
	})

	if err != nil {
		return nil, fmt.Errorf("cannot contact with AWS API: %w", err)
	}

	for _, r := range resp.Reservations {
		for _, inst := range r.Instances {
			if *inst.PrivateIpAddress == info.ipAddress {
				return &inst, nil
			}
		}
	}
	return nil, nil
}

func connectToInstance(ctx context.Context, params []string) error {
	cmd := exec.CommandContext(ctx, "ssh", params...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stdout
	cmd.Stdin = os.Stdin

	if err := cmd.Run(); err != nil {
		if exiterr, ok := err.(*exec.ExitError); ok {
			// terminated by Control-C so ignoring
			if exiterr.ExitCode() == 130 {
				return nil
			}
		}

		return fmt.Errorf("error while connecting to the instance: %w", err)
	}

	return nil
}

func strp(str string) *string {
	return &str
}
