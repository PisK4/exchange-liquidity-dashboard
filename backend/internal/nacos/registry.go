package nacos

import (
	"github.com/nacos-group/nacos-sdk-go/clients"
	"github.com/nacos-group/nacos-sdk-go/clients/config_client"
	"github.com/nacos-group/nacos-sdk-go/common/constant"
	"github.com/nacos-group/nacos-sdk-go/vo"
)

type Registry struct {
	cfg    BootstrapConfig
	client config_client.IConfigClient
}

func NewRegistry(cfg BootstrapConfig) (*Registry, error) {
	if err := cfg.validate(); err != nil {
		return nil, err
	}
	clientConfig := constant.ClientConfig{
		NamespaceId:         cfg.ClientNamespaceID,
		TimeoutMs:           5000,
		NotLoadCacheAtStart: true,
		LogDir:              cfg.ClientLogDir,
		CacheDir:            cfg.ClientCacheDir,
		LogLevel:            cfg.ClientLogLevel,
		Username:            cfg.Username,
		Password:            cfg.Password,
	}
	serverConfigs := []constant.ServerConfig{{
		IpAddr: cfg.ServerIP,
		Port:   cfg.ServerPort,
		Scheme: "http",
	}}
	client, err := clients.NewConfigClient(vo.NacosClientParam{
		ClientConfig:  &clientConfig,
		ServerConfigs: serverConfigs,
	})
	if err != nil {
		return nil, err
	}
	return &Registry{cfg: cfg, client: client}, nil
}

func (r *Registry) GetConfig() (string, error) {
	return r.client.GetConfig(vo.ConfigParam{
		DataId: r.cfg.ConfigName,
		Group:  r.cfg.GroupName,
	})
}

func (r *Registry) ListenConfig(onChange func(data string)) error {
	return r.client.ListenConfig(vo.ConfigParam{
		DataId: r.cfg.ConfigName,
		Group:  r.cfg.GroupName,
		OnChange: func(_, _, _, data string) {
			if onChange != nil {
				onChange(data)
			}
		},
	})
}
