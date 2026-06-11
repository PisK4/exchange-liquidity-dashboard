package nacos

import (
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

type BootstrapConfig struct {
	Region            string `yaml:"Region"`
	ServerIP          string `yaml:"ServerIP"`
	ServerPort        uint64 `yaml:"ServerPort"`
	ClientLogDir      string `yaml:"ClientLogDir"`
	ClientCacheDir    string `yaml:"ClientCacheDir"`
	ClientLogLevel    string `yaml:"ClientLogLevel"`
	Username          string `yaml:"Username"`
	Password          string `yaml:"Password"`
	ClientNamespaceID string `yaml:"ClientNamespaceID"`
	ClusterName       string `yaml:"ClusterName"`
	GroupName         string `yaml:"GroupName"`
	ConfigName        string `yaml:"ConfigName"`
}

func LoadBootstrap(configDir string) (BootstrapConfig, error) {
	if configDir == "" {
		configDir = filepath.Join("..", "config")
	}
	cfg := DefaultBootstrapConfig()
	path := filepath.Join(configDir, "nacos.yaml")
	data, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		return BootstrapConfig{}, err
	}
	if err == nil {
		if err := yaml.Unmarshal(data, &cfg); err != nil {
			return BootstrapConfig{}, err
		}
	}
	applyEnvOverrides(&cfg)
	return cfg, cfg.validate()
}

func DefaultBootstrapConfig() BootstrapConfig {
	return BootstrapConfig{
		Region:            "ap-southeast-1",
		ServerIP:          "127.0.0.1",
		ServerPort:        8848,
		ClientLogDir:      "./nacos/log",
		ClientCacheDir:    "./nacos/cache",
		ClientLogLevel:    "error",
		ClientNamespaceID: "dev",
		ClusterName:       "DEFAULT",
		GroupName:         "DEFAULT_GROUP",
		ConfigName:        "edgex-ops-intelligence.yaml",
	}
}

func applyEnvOverrides(cfg *BootstrapConfig) {
	if v := strings.TrimSpace(os.Getenv("NACOS_CONFIG_SERVER_ADDR")); v != "" {
		if host, port, ok := splitServerAddr(v); ok {
			cfg.ServerIP = host
			cfg.ServerPort = port
		}
	}
	if v := strings.TrimSpace(os.Getenv("NACOS_CONFIG_SERVER_IP")); v != "" {
		cfg.ServerIP = v
	}
	if v := strings.TrimSpace(os.Getenv("NACOS_CONFIG_SERVER_PORT")); v != "" {
		if port, err := strconv.ParseUint(v, 10, 64); err == nil && port > 0 {
			cfg.ServerPort = port
		}
	}
	if v := strings.TrimSpace(os.Getenv("NACOS_CONFIG_NAMESPACE")); v != "" {
		cfg.ClientNamespaceID = v
	}
	if v := strings.TrimSpace(os.Getenv("NACOS_CONFIG_GROUP")); v != "" {
		cfg.GroupName = v
	}
	if v := strings.TrimSpace(os.Getenv("NACOS_CONFIG_NAME")); v != "" {
		cfg.ConfigName = v
	}
	if v := strings.TrimSpace(os.Getenv("NACOS_CONFIG_USERNAME")); v != "" {
		cfg.Username = v
	}
	if v := strings.TrimSpace(os.Getenv("NACOS_CONFIG_PASSWORD")); v != "" {
		cfg.Password = v
	}
}

func splitServerAddr(raw string) (string, uint64, bool) {
	host, portRaw, err := net.SplitHostPort(raw)
	if err != nil {
		parts := strings.Split(raw, ":")
		if len(parts) != 2 {
			return "", 0, false
		}
		host, portRaw = parts[0], parts[1]
	}
	port, err := strconv.ParseUint(portRaw, 10, 64)
	if err != nil || strings.TrimSpace(host) == "" || port == 0 {
		return "", 0, false
	}
	return host, port, true
}

func (c BootstrapConfig) validate() error {
	if strings.TrimSpace(c.ServerIP) == "" {
		return fmt.Errorf("nacos server ip is empty")
	}
	if c.ServerPort == 0 {
		return fmt.Errorf("nacos server port is empty")
	}
	if strings.TrimSpace(c.GroupName) == "" {
		return fmt.Errorf("nacos group name is empty")
	}
	if strings.TrimSpace(c.ConfigName) == "" {
		return fmt.Errorf("nacos config name is empty")
	}
	return nil
}
