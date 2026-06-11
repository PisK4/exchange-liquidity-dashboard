package secrets

import (
	"context"
	"fmt"
	"os"
	"strings"

	"edgex-ops-intelligence/backend/internal/config"
)

type Resolver interface {
	Get(key string) (string, error)
}

func Resolve(ctx context.Context, cfg *config.Config, resolver Resolver) error {
	_ = ctx
	if cfg == nil {
		return nil
	}
	if !smEnabled() {
		return nil
	}
	if resolver == nil {
		resolver = AWSResolver{}
	}
	prodMode := isProdMode()
	resolve := func(target *string, key, label string) error {
		key = strings.TrimSpace(key)
		if key == "" {
			return nil
		}
		value, err := resolver.Get(key)
		if err != nil {
			if prodMode {
				return fmt.Errorf("resolve %s from AWS SM key %q: %w", label, key, err)
			}
			return nil
		}
		*target = value
		return nil
	}
	if err := resolve(&cfg.Database.Addr, cfg.Aws.DBAddr, "database addr"); err != nil {
		return err
	}
	if err := resolve(&cfg.Database.UserName, cfg.Aws.DBUser, "database user"); err != nil {
		return err
	}
	if err := resolve(&cfg.Database.Password, cfg.Aws.DBPass, "database password"); err != nil {
		return err
	}
	if err := resolve(&cfg.Runtime.CoinGecko.APIKey, cfg.Aws.CoinGeckoAPIKey, "coingecko api key"); err != nil {
		return err
	}
	if err := resolve(&cfg.Runtime.ListingAgent.DecisionCard.Callback.Secret, cfg.Aws.ListingCallbackSecret, "listing callback secret"); err != nil {
		return err
	}
	if err := resolve(&cfg.Runtime.ActivityAgent.DecisionToken.Secret, cfg.Aws.ActivityDecisionToken, "activity decision token"); err != nil {
		return err
	}
	return nil
}

func smEnabled() bool {
	v := strings.ToLower(strings.TrimSpace(os.Getenv("AWS_SM_ENABLE")))
	return v == "true" || v == "1" || v == "yes"
}

func isProdMode() bool {
	if strings.ToLower(strings.TrimSpace(os.Getenv("USE_LOCAL_CONFIG"))) == "false" {
		return true
	}
	return strings.ToLower(strings.TrimSpace(os.Getenv("OPS_INTELLIGENCE_CONFIG_SOURCE"))) == "nacos"
}
