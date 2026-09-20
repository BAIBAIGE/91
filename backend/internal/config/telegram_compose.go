package config

import (
	"errors"
	"os"
	"path"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

const telegramDataMount = "/var/lib/telegram-bot-api"

type telegramDeployment struct {
	apiBaseURL         string
	apiRoot, localRoot string
}

// LoadTelegramCompose reads the deployment once at startup. YAML hot reloads
// cannot override its endpoint or paths; deployment changes require a restart.
func (m *Manager) LoadTelegramCompose(filename string) error {
	deployment, err := readTelegramCompose(filename)
	m.mu.Lock()
	m.telegramDeployment, m.telegramDeploymentErr = deployment, err
	m.mu.Unlock()
	return err
}

func (m *Manager) TelegramDeploymentError() error {
	if m == nil {
		return errors.New("Telegram 部署配置不可用")
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.telegramDeploymentErr
}

type composeService struct {
	Volumes     []yaml.Node `yaml:"volumes"`
	Environment yaml.Node   `yaml:"environment"`
	Ports       []yaml.Node `yaml:"ports"`
	NetworkMode string      `yaml:"network_mode"`
	Command     yaml.Node   `yaml:"command"`
	Entrypoint  yaml.Node   `yaml:"entrypoint"`
}

type composeMount struct {
	Type     string `yaml:"type"`
	Source   string `yaml:"source"`
	Target   string `yaml:"target"`
	ReadOnly bool   `yaml:"read_only"`
}

func readTelegramCompose(filename string) (telegramDeployment, error) {
	var empty telegramDeployment
	filename, err := filepath.Abs(filename)
	if err != nil {
		return empty, errors.New("Telegram Compose 文件路径无效")
	}
	data, err := os.ReadFile(filename)
	if err != nil {
		return empty, errors.New("无法读取 Telegram Compose 文件，请检查部署文件路径和权限")
	}
	var document struct {
		Services map[string]composeService `yaml:"services"`
	}
	if err := yaml.Unmarshal(data, &document); err != nil {
		return empty, errors.New("Telegram Compose 文件格式无效")
	}
	bot, ok := document.Services["telegram-bot-api"]
	if !ok {
		return empty, errors.New("Telegram Compose 缺少 telegram-bot-api 服务")
	}
	mounts, err := readComposeMounts(bot.Volumes, filepath.Dir(filename))
	if err != nil {
		return empty, err
	}
	var storage *composeMount
	for n := range mounts {
		mount := &mounts[n]
		if strings.HasPrefix(mount.Target, telegramDataMount+"/") {
			return empty, errors.New("Telegram 数据目录不能包含独立的子目录挂载")
		}
		if mount.Target == telegramDataMount {
			if storage != nil || mount.ReadOnly {
				return empty, errors.New("Telegram 数据目录必须有唯一的可写挂载")
			}
			storage = mount
		}
	}
	if storage == nil {
		return empty, errors.New("Telegram Compose 缺少配套 Bot API 的数据目录挂载")
	}
	website, inDocker := document.Services["video-site-91"]
	if website.NetworkMode != "" {
		return empty, errors.New("Telegram 自动连接需要使用 Compose 服务网络，请移除网站的 network_mode")
	}
	apiBaseURL, err := telegramComposeEndpoint(bot, inDocker)
	if err != nil {
		return empty, err
	}
	if !inDocker {
		if storage.Type != "bind" {
			return empty, errors.New("原生网站的 Telegram 数据目录必须使用宿主机目录挂载")
		}
		return telegramDeployment{apiBaseURL: apiBaseURL, apiRoot: storage.Target, localRoot: storage.Source}, nil
	}
	// The Compose file describes both container namespaces. Match the shared
	// source, then use the website container's target rather than a host path.
	peers, err := readComposeMounts(website.Volumes, filepath.Dir(filename))
	if err != nil {
		return empty, err
	}
	var local *composeMount
	for n := range peers {
		mount := &peers[n]
		if mount.Type == storage.Type && mount.Source == storage.Source {
			if local != nil || mount.ReadOnly {
				return empty, errors.New("网站必须有唯一的可写 Telegram 共享数据挂载")
			}
			local = mount
		}
	}
	if local == nil {
		return empty, errors.New("网站与 Bot API 未挂载同一份 Telegram 数据")
	}
	for _, mount := range peers {
		if strings.HasPrefix(mount.Target, local.Target+"/") {
			return empty, errors.New("网站的 Telegram 数据目录不能包含独立的子目录挂载")
		}
	}
	return telegramDeployment{apiBaseURL: apiBaseURL, apiRoot: storage.Target, localRoot: filepath.FromSlash(local.Target)}, nil
}

func readComposeMounts(nodes []yaml.Node, directory string) ([]composeMount, error) {
	mounts := make([]composeMount, 0, len(nodes))
	targets := make(map[string]bool)
	for _, node := range nodes {
		var mount composeMount
		if node.Kind == yaml.ScalarNode && node.Tag == "!!str" {
			parts := strings.Split(node.Value, ":")
			if len(parts) < 2 || len(parts) > 3 {
				return nil, errors.New("Telegram Compose 请使用明确的 source:target 挂载或长格式挂载")
			}
			mount.Source, mount.Target = parts[0], parts[1]
			if len(parts) == 3 {
				for _, option := range strings.Split(parts[2], ",") {
					mount.ReadOnly = mount.ReadOnly || option == "ro"
				}
			}
			mount.Type = "volume"
			if filepath.IsAbs(mount.Source) || strings.HasPrefix(mount.Source, "./") || strings.HasPrefix(mount.Source, "../") {
				mount.Type = "bind"
			}
		} else if node.Kind != yaml.MappingNode || node.Decode(&mount) != nil {
			return nil, errors.New("Telegram Compose 挂载格式无效")
		}
		if mount.Source == "" || !path.IsAbs(mount.Target) || strings.ContainsAny(mount.Source+mount.Target, "$\x00") || strings.Contains(mount.Target, "\\") {
			return nil, errors.New("Telegram Compose 挂载必须填写明确的源和容器路径，不支持目录变量")
		}
		mount.Target = path.Clean(mount.Target)
		if targets[mount.Target] {
			return nil, errors.New("Telegram Compose 不能重复挂载同一个容器目录")
		}
		targets[mount.Target] = true
		switch mount.Type {
		case "bind":
			if strings.HasPrefix(mount.Source, "~") {
				return nil, errors.New("Telegram Compose 宿主机目录请填写绝对路径或相对路径")
			}
			if !filepath.IsAbs(mount.Source) {
				mount.Source = filepath.Join(directory, mount.Source)
			}
			mount.Source = filepath.Clean(mount.Source)
		case "volume":
			if strings.ContainsAny(mount.Source, "/\\") || mount.Source == "." || mount.Source == ".." {
				return nil, errors.New("Telegram Compose 数据卷名称无效")
			}
		default:
			return nil, errors.New("Telegram Compose 数据挂载仅支持 bind 和 volume")
		}
		mounts = append(mounts, mount)
	}
	return mounts, nil
}

func validateTelegramDeploymentFieldsRemoved(data []byte) error {
	var document struct {
		Telegram map[string]any `yaml:"telegram"`
	}
	if err := yaml.Unmarshal(data, &document); err != nil {
		return err
	}
	for _, name := range []string{"api_base_url", "api_files_root", "local_files_root"} {
		if _, exists := document.Telegram[name]; exists {
			return errors.New("Telegram 连接地址和文件目录由 Compose 自动配置，请移除网站 YAML 中的 " + name)
		}
	}
	return nil
}
