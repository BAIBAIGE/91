package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestTelegramComposeResolvesEndpoints(t *testing.T) {
	for _, tc := range []struct {
		name, environment, ports, want string
		inDocker                       bool
	}{
		{"container custom port", "{TELEGRAM_HTTP_PORT: 9000}", "['127.0.0.1:8888:9000']", "http://telegram-bot-api:9000", true},
		{"container no published port", "['TELEGRAM_HTTP_PORT=9000']", "[]", "http://telegram-bot-api:9000", true},
		{"native custom host port", "{TELEGRAM_HTTP_PORT: '7878'}", "['127.0.0.1:8888:7878']", "http://127.0.0.1:8888", false},
		{"native custom listening port", "['TELEGRAM_HTTP_PORT=9000']", "['8888:9000/tcp']", "http://127.0.0.1:8888", false},
		{"native wildcard", "{TELEGRAM_HTTP_PORT: 7878}", "['0.0.0.0:8888:7878']", "http://127.0.0.1:8888", false},
		{"native long port", "{TELEGRAM_HTTP_PORT: 7878}", "[{target: 7878, published: '8888', host_ip: 127.0.0.1, protocol: tcp}]", "http://127.0.0.1:8888", false},
		{"native bound interface", "{TELEGRAM_HTTP_PORT: 7878}", "[{target: 7878, published: 8888, host_ip: 192.0.2.1}]", "http://192.0.2.1:8888", false},
		{"native IPv6", "{TELEGRAM_HTTP_PORT: 7878}", "['[::1]:8888:7878']", "http://[::1]:8888", false},
		{"native IPv6 wildcard", "{TELEGRAM_HTTP_PORT: 7878}", "[{target: 7878, published: 8888, host_ip: '::'}]", "http://[::1]:8888", false},
		{"native matching TCP port", "{TELEGRAM_HTTP_PORT: 7878}", "['9001:9000', '8888:7878/udp', '8888:7878/tcp']", "http://127.0.0.1:8888", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			filename := writeEndpointCompose(t, tc.environment, tc.ports, "", tc.inDocker)
			got, err := readTelegramCompose(filename)
			if err != nil || got.apiBaseURL != tc.want {
				t.Fatalf("endpoint=%q error=%v, want %q", got.apiBaseURL, err, tc.want)
			}
		})
	}
}

func TestTelegramComposeRejectsUnresolvableEndpoints(t *testing.T) {
	for _, tc := range []struct {
		name, environment, ports, extra, message string
		inDocker                                 bool
	}{
		{"missing port", "{}", "[]", "", "TELEGRAM_HTTP_PORT", true},
		{"inherited port", "['TELEGRAM_HTTP_PORT']", "[]", "", "TELEGRAM_HTTP_PORT", true},
		{"variable port", "{TELEGRAM_HTTP_PORT: '${BOT_PORT:-7878}'}", "[]", "", "TELEGRAM_HTTP_PORT", true},
		{"zero port", "{TELEGRAM_HTTP_PORT: 0}", "[]", "", "TELEGRAM_HTTP_PORT", true},
		{"invalid port", "{TELEGRAM_HTTP_PORT: 65536}", "[]", "", "TELEGRAM_HTTP_PORT", true},
		{"duplicate port variable", "['TELEGRAM_HTTP_PORT=7878', 'TELEGRAM_HTTP_PORT=9000']", "[]", "", "重复", true},
		{"invalid environment", "[{TELEGRAM_HTTP_PORT: 7878}]", "[]", "", "environment", true},
		{"missing mapping", "{TELEGRAM_HTTP_PORT: 7878}", "[]", "", "映射", false},
		{"wrong target", "{TELEGRAM_HTTP_PORT: 7878}", "['8888:9000']", "", "映射", false},
		{"UDP only", "{TELEGRAM_HTTP_PORT: 7878}", "['8888:7878/udp']", "", "TCP", false},
		{"multiple mappings", "{TELEGRAM_HTTP_PORT: 7878}", "['8888:7878', '9999:7878']", "", "多个", false},
		{"random host port", "{TELEGRAM_HTTP_PORT: 7878}", "['7878']", "", "随机", false},
		{"long random host port", "{TELEGRAM_HTTP_PORT: 7878}", "[{target: 7878}]", "", "随机", false},
		{"variable host port", "{TELEGRAM_HTTP_PORT: 7878}", "['${HOST_PORT}:7878']", "", "变量", false},
		{"port range", "{TELEGRAM_HTTP_PORT: 7878}", "['8888-8890:7878']", "", "范围", false},
		{"variable host", "{TELEGRAM_HTTP_PORT: 7878}", "['${HOST_IP}:8888:7878']", "", "host_ip", false},
		{"host networking", "{TELEGRAM_HTTP_PORT: 7878}", "[]", "    network_mode: host\n", "network_mode", true},
		{"command override", "{TELEGRAM_HTTP_PORT: 7878}", "[]", "    command: ['--http-port=9000']\n", "command", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			filename := writeEndpointCompose(t, tc.environment, tc.ports, tc.extra, tc.inDocker)
			got, err := readTelegramCompose(filename)
			if err == nil || !strings.Contains(err.Error(), tc.message) || got != (telegramDeployment{}) {
				t.Fatalf("deployment=%+v error=%v, want error containing %q", got, err, tc.message)
			}
		})
	}
}

func writeEndpointCompose(t *testing.T, environment, ports, extra string, inDocker bool) string {
	t.Helper()
	filename := filepath.Join(t.TempDir(), "telegram.yml")
	document := "services:\n  telegram-bot-api:\n    environment: " + environment + "\n    ports: " + ports + "\n" + extra +
		"    volumes: ['/data/tg:/var/lib/telegram-bot-api']\n"
	if inDocker {
		document += "  video-site-91:\n    volumes: ['/data/tg:/var/lib/telegram-bot-api']\n"
	}
	if err := os.WriteFile(filename, []byte(document), 0600); err != nil {
		t.Fatal(err)
	}
	return filename
}
