package smsprovider

import (
	"context"
	"os"
	"sync"

	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/pinpointsmsvoicev2"
)

var (
	defaultAEUMOnce sync.Once
	defaultAEUM     *AEUMProvider
	defaultAEUMErr  error
)

// DefaultAEUM lazily builds the global AEUMProvider from environment variables
// and registers it in the package registry under DeviceTypeAEUM. Returns nil
// when AEUM_ENABLED is not truthy. Safe for concurrent use.
func DefaultAEUM() *AEUMProvider {
	defaultAEUMOnce.Do(func() {
		defaultAEUM, defaultAEUMErr = buildDefaultAEUM()
		if defaultAEUM != nil {
			Register(DeviceTypeAEUM, defaultAEUM)
		}
	})
	return defaultAEUM
}

// DefaultAEUMError exposes any init error encountered while building the
// default provider; useful for logging at startup.
func DefaultAEUMError() error { return defaultAEUMErr }

func buildDefaultAEUM() (*AEUMProvider, error) {
	if !aeumEnabledFromEnv() {
		return nil, nil
	}
	region := os.Getenv("AEUM_REGION")
	awsCfg, err := config.LoadDefaultConfig(context.Background(), config.WithRegion(region))
	if err != nil {
		return nil, err
	}
	client := pinpointsmsvoicev2.NewFromConfig(awsCfg)

	originationARN := os.Getenv("AEUM_ORIGINATION_IDENTITY_ARN")
	if originationARN == "" {
		originationARN = os.Getenv("AEUM_ORIGINATION_POOL_ARN")
	}

	return NewAEUMProvider(AEUMConfig{
		Enabled:                true,
		OriginationIdentityARN: originationARN,
		ConfigurationSetName:   os.Getenv("AEUM_CONFIGURATION_SET_NAME"),
		Region:                 region,
		Client:                 client,
	}), nil
}
