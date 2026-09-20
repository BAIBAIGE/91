package config

import (
	"errors"
	"net"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

// Resolve the address from the website's network namespace. A container uses
// the service's listening port; a native website uses its published host port.
func telegramComposeEndpoint(bot composeService, inDocker bool) (string, error) {
	if bot.NetworkMode != "" {
		return "", errors.New("Telegram 自动连接需要使用 Compose 服务网络，请移除 Bot API 的 network_mode")
	}
	for _, override := range []yaml.Node{bot.Command, bot.Entrypoint} {
		if override.Kind != 0 && override.Tag != "!!null" {
			return "", errors.New("Telegram 自动连接不支持自定义 command 或 entrypoint，请通过 TELEGRAM_HTTP_PORT 设置监听端口")
		}
	}
	rawPort, err := composeEnvironmentValue(bot.Environment, "TELEGRAM_HTTP_PORT")
	if err != nil {
		return "", err
	}
	port, err := composeTCPPort(rawPort)
	if err != nil {
		return "", errors.New("Telegram Compose 的 TELEGRAM_HTTP_PORT 必须明确填写 1–65535 的端口号，不支持变量")
	}
	if inDocker {
		return "http://" + net.JoinHostPort("telegram-bot-api", port), nil
	}

	endpoint := ""
	for _, node := range bot.Ports {
		mapping, err := readComposePort(node)
		if err != nil {
			return "", err
		}
		if mapping.Protocol == "udp" || mapping.Target != port {
			continue
		}
		if endpoint != "" {
			return "", errors.New("Telegram Bot API 监听端口存在多个宿主机映射，请保留唯一的 TCP 端口映射")
		}
		host := mapping.HostIP
		switch host {
		case "", "0.0.0.0":
			host = "127.0.0.1"
		case "::":
			host = "::1"
		}
		endpoint = "http://" + net.JoinHostPort(host, mapping.Published)
	}
	if endpoint == "" {
		return "", errors.New("原生网站需要在 Telegram Compose 的 ports 中明确映射 TELEGRAM_HTTP_PORT 对应的 TCP 端口")
	}
	return endpoint, nil
}

func composeEnvironmentValue(node yaml.Node, key string) (string, error) {
	switch node.Kind {
	case 0:
		return "", nil
	case yaml.MappingNode:
		var values map[string]string
		if err := node.Decode(&values); err == nil {
			return values[key], nil
		}
	case yaml.SequenceNode:
		var entries []string
		if err := node.Decode(&entries); err != nil {
			break
		}
		value, found := "", false
		for _, entry := range entries {
			name, text, _ := strings.Cut(entry, "=")
			if name != key {
				continue
			}
			if found {
				return "", errors.New("Telegram Compose 重复设置了 " + key)
			}
			value, found = text, true
		}
		return value, nil
	}
	return "", errors.New("Telegram Compose 的 environment 必须使用键值映射或 KEY=value 列表")
}

type composePort struct {
	Target    string `yaml:"target"`
	Published string `yaml:"published"`
	HostIP    string `yaml:"host_ip"`
	Protocol  string `yaml:"protocol"`
}

func readComposePort(node yaml.Node) (composePort, error) {
	var mapping composePort
	invalid := errors.New("Telegram Compose 的 ports 必须使用明确的宿主机端口和容器端口，不支持变量、范围或随机端口")
	if node.Kind == yaml.ScalarNode && node.Tag == "!!str" {
		binding, protocol, _ := strings.Cut(node.Value, "/")
		mapping.Protocol = protocol
		separator := strings.LastIndex(binding, ":")
		if separator < 0 {
			return mapping, invalid
		}
		mapping.Target = binding[separator+1:]
		hostPort := binding[:separator]
		if strings.Contains(hostPort, ":") {
			var err error
			mapping.HostIP, mapping.Published, err = net.SplitHostPort(hostPort)
			if err != nil {
				return mapping, invalid
			}
		} else {
			mapping.Published = hostPort
		}
	} else if node.Kind != yaml.MappingNode || node.Decode(&mapping) != nil {
		return mapping, invalid
	}
	var err error
	mapping.Target, err = composeTCPPort(mapping.Target)
	if err != nil {
		return mapping, invalid
	}
	mapping.Published, err = composeTCPPort(mapping.Published)
	if err != nil {
		return mapping, invalid
	}
	if mapping.HostIP != "" && net.ParseIP(mapping.HostIP) == nil {
		return mapping, errors.New("Telegram Compose 的 host_ip 必须填写明确的 IP 地址")
	}
	if mapping.Protocol != "" && mapping.Protocol != "tcp" && mapping.Protocol != "udp" {
		return mapping, errors.New("Telegram Compose 的端口协议仅支持 tcp 或 udp")
	}
	return mapping, nil
}

func composeTCPPort(value string) (string, error) {
	port, err := strconv.ParseUint(value, 10, 16)
	if err != nil || port == 0 {
		return "", errors.New("无效的端口号")
	}
	return strconv.FormatUint(port, 10), nil
}
