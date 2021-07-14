package main

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io/ioutil"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"strings"
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
		}

		res[parts[0]] = append(res[parts[0]], strings.Join(parts[1:], " "))
	}

	return res, nil
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
