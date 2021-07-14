package main

import (
	"context"
	"fmt"
	"net"
	"os"
	"os/exec"

	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	"github.com/aws/aws-sdk-go-v2/service/ec2/types"
	"github.com/aws/aws-sdk-go-v2/service/ec2instanceconnect"
)

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
