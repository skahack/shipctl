package cmd

import "os"

func getAWSRegion() string {
	if os.Getenv("AWS_REGION") != "" {
		return os.Getenv("AWS_REGION")
	}

	if os.Getenv("AWS_DEFAULT_REGION") != "" {
		return os.Getenv("AWS_DEFAULT_REGION")
	}

	return ""
}
