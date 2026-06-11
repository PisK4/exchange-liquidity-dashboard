package secrets

import (
	"context"
	"encoding/json"
	"fmt"
	"os"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/secretsmanager"
)

type AWSResolver struct{}

func (AWSResolver) Get(key string) (string, error) {
	return GetSMByKey(key)
}

func GetSMByKey(key string) (string, error) {
	return GetSMKey(os.Getenv("AWS_SM_REGION"), os.Getenv("AWS_SM_SECRET_ID"), key)
}

func GetSMKey(region, secretName, key string) (string, error) {
	if region == "" {
		return "", fmt.Errorf("AWS_SM_REGION is empty")
	}
	if secretName == "" {
		return "", fmt.Errorf("AWS_SM_SECRET_ID is empty")
	}
	if key == "" {
		return "", fmt.Errorf("aws secrets manager key is empty")
	}

	cfg, err := awsconfig.LoadDefaultConfig(context.TODO(), awsconfig.WithRegion(region))
	if err != nil {
		return "", fmt.Errorf("load aws config failed: %w", err)
	}
	svc := secretsmanager.NewFromConfig(cfg)
	result, err := svc.GetSecretValue(context.TODO(), &secretsmanager.GetSecretValueInput{
		SecretId:     aws.String(secretName),
		VersionStage: aws.String("AWSCURRENT"),
	})
	if err != nil {
		return "", fmt.Errorf("get secret value failed: %w", err)
	}
	if result.SecretString == nil {
		return "", fmt.Errorf("secret string is empty")
	}

	var values map[string]string
	if err := json.Unmarshal([]byte(*result.SecretString), &values); err != nil {
		return "", fmt.Errorf("parse secret string failed: %w", err)
	}
	value, ok := values[key]
	if !ok {
		return "", fmt.Errorf("key %s not found in secret", key)
	}
	return value, nil
}
