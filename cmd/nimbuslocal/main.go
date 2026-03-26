package main

import (
	"fmt"
	"os"
	"os/exec"
	"syscall"
)

const usage = `nimbuslocal - AWS CLI wrapper for Nimbus local development

Usage:
  nimbuslocal <aws-cli-command> [options]

Examples:
  nimbuslocal s3 mb s3://my-bucket
  nimbuslocal s3 ls
  nimbuslocal s3 cp ./file.txt s3://my-bucket/file.txt
  nimbuslocal sqs create-queue --queue-name my-queue
  nimbuslocal sqs send-message --queue-url http://... --message-body "hello"
  nimbuslocal dynamodb list-tables

Environment:
  NIMBUS_ENDPOINT_URL   Override the Nimbus endpoint (default: http://localhost:4566)
  AWS_ACCESS_KEY_ID     Accepted but not validated (default: test)
  AWS_SECRET_ACCESS_KEY Accepted but not validated (default: test)
  AWS_DEFAULT_REGION    Region (default: us-east-1)

nimbuslocal is equivalent to running:
  aws --endpoint-url=<NIMBUS_ENDPOINT_URL> <command>
`

func main() {
	args := os.Args[1:]

	if len(args) == 0 {
		fmt.Print(usage)
		os.Exit(0)
	}

	if args[0] == "--help" || args[0] == "-h" {
		fmt.Print(usage)
		os.Exit(0)
	}

	if args[0] == "--version" {
		fmt.Println("nimbuslocal 0.1.0")
		os.Exit(0)
	}

	endpoint := endpointURL()

	// Ensure dummy credentials are set if not provided — aws CLI requires them
	// even for local use. Nimbus accepts any value.
	if os.Getenv("AWS_ACCESS_KEY_ID") == "" {
		os.Setenv("AWS_ACCESS_KEY_ID", "test")
	}
	if os.Getenv("AWS_SECRET_ACCESS_KEY") == "" {
		os.Setenv("AWS_SECRET_ACCESS_KEY", "test")
	}
	if os.Getenv("AWS_DEFAULT_REGION") == "" {
		os.Setenv("AWS_DEFAULT_REGION", "us-east-1")
	}

	// Build the aws CLI invocation, injecting the endpoint URL
	// We prepend --endpoint-url so it applies globally to all services
	awsArgs := append([]string{"--endpoint-url", endpoint}, args...)

	cmd := exec.Command("aws", awsArgs...)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	if err := cmd.Run(); err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			if status, ok := exitErr.Sys().(syscall.WaitStatus); ok {
				os.Exit(status.ExitStatus())
			}
		}
		// aws CLI not found
		if err == exec.ErrNotFound {
			fmt.Fprintln(os.Stderr, "error: 'aws' CLI not found. Install it with:")
			fmt.Fprintln(os.Stderr, "  pip install awscli")
			fmt.Fprintln(os.Stderr, "  brew install awscli")
			os.Exit(1)
		}
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func endpointURL() string {
	// Check environment override first
	if v := os.Getenv("NIMBUS_ENDPOINT_URL"); v != "" {
		return v
	}
	// Fallback to LocalStack-compatible env var so existing scripts work
	if v := os.Getenv("LOCALSTACK_ENDPOINT_URL"); v != "" {
		return v
	}
	return "http://localhost:4566"
}
