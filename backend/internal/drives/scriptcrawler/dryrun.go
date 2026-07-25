package scriptcrawler

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// DryRun 在不入库的前提下试跑一个爬虫脚本：临时目录里生成 job.json，
// 启动脚本进程，拿到第一条（或前 MaxItems 条）item 事件后立即停止，
// 再对视频直链做一次小范围探测，验证脚本"能不能爬取到视频"。
// 用于后台导入脚本后的"测试脚本"按钮。

const (
	defaultDryRunTimeout  = 2 * time.Minute
	dryRunLogTailLines    = 60
	dryRunMediaProbeLimit = 20 * time.Second
	dryRunStopGrace       = 100 * time.Millisecond
)

type DryRunConfig struct {
	PythonPath string
	ScriptPath string
	ProxyURL   string
	ConfigJSON string
	// MaxItems 收到多少条 item 后停止脚本，默认 1。
	MaxItems int
	// Timeout 整个试跑的硬上限，默认 2 分钟。
	Timeout time.Duration
	// SkipMediaProbe 跳过视频直链可达性探测（单测注入用）。
	SkipMediaProbe bool
	HTTPClient     *http.Client
	MaxStdoutBytes int64
	MaxStderrBytes int64
}

type DryRunItem struct {
	Title          string `json:"title"`
	SourceID       string `json:"sourceId,omitempty"`
	MediaURL       string `json:"mediaUrl,omitempty"`
	MediaLocalFile string `json:"mediaLocalFile,omitempty"`
	ThumbnailURL   string `json:"thumbnailUrl,omitempty"`
	DetailURL      string `json:"detailUrl,omitempty"`
}

type DryRunMediaCheck struct {
	OK            bool   `json:"ok"`
	Status        int    `json:"status,omitempty"`
	ContentType   string `json:"contentType,omitempty"`
	ContentLength int64  `json:"contentLengthBytes,omitempty"`
	Error         string `json:"error,omitempty"`
}

type DryRunResult struct {
	OK         bool              `json:"ok"`
	Protocol   string            `json:"protocol,omitempty"`
	Items      []DryRunItem      `json:"items"`
	MediaCheck *DryRunMediaCheck `json:"mediaCheck,omitempty"`
	Error      string            `json:"error,omitempty"`
	Log        []string          `json:"log,omitempty"`
	DurationMs int64             `json:"durationMs"`
}

type dryRunLogTail struct {
	mu         sync.Mutex
	lines      []string
	partial    string
	totalBytes int64
	suppressed int64
	maxBytes   int64
}

func newDryRunLogTail(maxBytes int64) *dryRunLogTail {
	if maxBytes <= 0 {
		maxBytes = defaultMaxStderrBytes
	}
	return &dryRunLogTail{
		lines:    make([]string, 0, dryRunLogTailLines),
		maxBytes: maxBytes,
	}
}

func (t *dryRunLogTail) Write(p []byte) (int, error) {
	t.mu.Lock()
	defer t.mu.Unlock()

	originalLen := len(p)
	remaining := t.maxBytes - t.totalBytes
	if remaining <= 0 {
		t.suppressed += int64(originalLen)
		return originalLen, nil
	}
	accepted := p
	if int64(len(accepted)) > remaining {
		accepted = accepted[:remaining]
		t.suppressed += int64(originalLen - len(accepted))
	}
	t.totalBytes += int64(len(accepted))
	chunk := strings.ReplaceAll(string(accepted), "\r\n", "\n")
	parts := strings.Split(t.partial+chunk, "\n")
	t.partial = truncateDryRunLogLine(parts[len(parts)-1])
	for _, line := range parts[:len(parts)-1] {
		t.appendLocked(line)
	}
	return originalLen, nil
}

func (t *dryRunLogTail) snapshot() []string {
	t.mu.Lock()
	defer t.mu.Unlock()

	lines := append([]string{}, t.lines...)
	if partial := strings.TrimSpace(t.partial); partial != "" {
		lines = appendDryRunLogLine(lines, partial)
	}
	if t.suppressed > 0 {
		lines = appendDryRunLogLine(lines, fmt.Sprintf("[stderr truncated: %d bytes suppressed]", t.suppressed))
	}
	return lines
}

func (t *dryRunLogTail) appendLocked(line string) {
	t.lines = appendDryRunLogLine(t.lines, line)
}

func appendDryRunLogLine(lines []string, line string) []string {
	line = truncateDryRunLogLine(strings.TrimSpace(line))
	if line == "" {
		return lines
	}
	if len(lines) >= dryRunLogTailLines {
		lines = lines[1:]
	}
	return append(lines, line)
}

func truncateDryRunLogLine(line string) string {
	if len(line) <= maxStderrLineBytes {
		return line
	}
	return line[:maxStderrLineBytes] + "…"
}

func DryRun(ctx context.Context, cfg DryRunConfig) *DryRunResult {
	started := time.Now()
	result := &DryRunResult{Items: []DryRunItem{}}
	defer func() { result.DurationMs = time.Since(started).Milliseconds() }()

	scriptPath := strings.TrimSpace(cfg.ScriptPath)
	if scriptPath == "" {
		result.Error = "脚本路径为空，请先导入脚本"
		return result
	}
	if _, err := os.Stat(scriptPath); err != nil {
		result.Error = fmt.Sprintf("脚本不存在: %v", err)
		return result
	}
	metadata, err := ReadMetadata(scriptPath)
	if err != nil {
		result.Error = fmt.Sprintf("脚本元信息无效: %v", err)
		return result
	}
	result.Protocol = metadata.Protocol
	pythonPath := strings.TrimSpace(cfg.PythonPath)
	if pythonPath == "" {
		pythonPath = "python3"
	}
	maxItems := cfg.MaxItems
	if maxItems <= 0 {
		maxItems = 1
	}
	timeout := cfg.Timeout
	if timeout <= 0 {
		timeout = defaultDryRunTimeout
	}
	maxStdoutBytes := cfg.MaxStdoutBytes
	if maxStdoutBytes <= 0 {
		maxStdoutBytes = defaultMaxStdoutBytes
	}

	tmpDir, err := os.MkdirTemp("", "crawler-dryrun-")
	if err != nil {
		result.Error = fmt.Sprintf("创建临时目录失败: %v", err)
		return result
	}
	defer os.RemoveAll(tmpDir)

	outputDir := filepath.Join(tmpDir, "output")
	if err := os.MkdirAll(outputDir, 0o755); err != nil {
		result.Error = fmt.Sprintf("创建输出目录失败: %v", err)
		return result
	}
	seenPath := filepath.Join(tmpDir, "seen.txt")
	if err := os.WriteFile(seenPath, nil, 0o644); err != nil {
		result.Error = fmt.Sprintf("写入 seen 文件失败: %v", err)
		return result
	}

	configJSON := json.RawMessage([]byte("{}"))
	if raw := strings.TrimSpace(cfg.ConfigJSON); raw != "" {
		if !json.Valid([]byte(raw)) {
			result.Error = "自定义配置必须是合法 JSON"
			return result
		}
		configJSON = json.RawMessage(raw)
	}
	job := Job{
		Protocol:          metadata.Protocol,
		Mode:              "crawl",
		RunID:             "dryrun-" + started.UTC().Format("20060102T150405Z"),
		CrawlerID:         "dryrun",
		TargetNew:         maxItems,
		UniqueTarget:      maxItems,
		CandidateBudget:   maxItems,
		SeenSourceIDsFile: seenPath,
		OutputDir:         outputDir,
		Config:            configJSON,
		Network:           JobNetwork{ProxyURL: strings.TrimSpace(cfg.ProxyURL)},
	}
	if metadata.Protocol == ProtocolV2 {
		job.Limits = &JobLimits{
			MaxRuntimeSeconds:           durationSeconds(timeout),
			DeadlineAt:                  started.Add(timeout).UTC().Format(time.RFC3339),
			ProgressIntervalSeconds:     durationSeconds(defaultProgressInterval),
			IdleTimeoutSeconds:          durationSeconds(defaultV2IdleTimeout),
			CandidateIdleTimeoutSeconds: durationSeconds(defaultCandidateIdle),
		}
	}
	jobPath := filepath.Join(tmpDir, "job.json")
	jobData, err := json.MarshalIndent(job, "", "  ")
	if err != nil {
		result.Error = fmt.Sprintf("生成 job 文件失败: %v", err)
		return result
	}
	if err := os.WriteFile(jobPath, jobData, 0o600); err != nil {
		result.Error = fmt.Sprintf("写入 job 文件失败: %v", err)
		return result
	}

	runCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	cmd := exec.CommandContext(runCtx, pythonPath, scriptPath, "--job", jobPath)
	cmd.Dir = filepath.Dir(scriptPath)
	setDryRunProcAttr(cmd)
	cmd.Cancel = func() error {
		return killDryRunProcess(cmd)
	}
	// 超时或提前 kill 后，脚本派生的子进程可能仍持有 stdout/stderr 管道；
	// WaitDelay 强制在宽限期后关闭管道，避免读取端永久阻塞。
	cmd.WaitDelay = 3 * time.Second
	if proxyURL := strings.TrimSpace(cfg.ProxyURL); proxyURL != "" {
		cmd.Env = append(os.Environ(),
			"HTTP_PROXY="+proxyURL,
			"HTTPS_PROXY="+proxyURL,
			"http_proxy="+proxyURL,
			"https_proxy="+proxyURL,
			"NO_PROXY=",
			"no_proxy=",
		)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		result.Error = fmt.Sprintf("启动脚本失败: %v", err)
		return result
	}
	logTail := newDryRunLogTail(cfg.MaxStderrBytes)
	cmd.Stderr = logTail
	if err := cmd.Start(); err != nil {
		_ = stdout.Close()
		result.Error = fmt.Sprintf("启动脚本失败: %v", err)
		return result
	}

	items := []DryRunItem{}
	var firstMediaHeaders map[string]string
	parseFailures := 0
	v2ItemsSeen := 0
	scanner := bufio.NewScanner(stdout)
	maxLineBytes := maxV1StdoutLineBytes
	strictV2 := metadata.Protocol == ProtocolV2
	if strictV2 {
		maxLineBytes = maxV2StdoutLineBytes
	}
	scanner.Buffer(make([]byte, 64*1024), maxLineBytes)
	var stdoutBytes int64
	for scanner.Scan() {
		if runCtx.Err() != nil {
			break
		}
		rawLine := scanner.Text()
		stdoutBytes += int64(len(rawLine)) + 1
		if stdoutBytes > maxStdoutBytes {
			result.Error = fmt.Sprintf("脚本 stdout 超过 %d 字节限制", maxStdoutBytes)
			break
		}
		line := strings.TrimSpace(rawLine)
		if line == "" {
			if strictV2 {
				result.Error = "crawler.v2 stdout 不能包含空行"
				break
			}
			continue
		}
		var event Event
		if err := json.Unmarshal([]byte(line), &event); err != nil {
			parseFailures++
			if strictV2 {
				result.Error = fmt.Sprintf("crawler.v2 stdout 必须全部为 JSON 对象: %v", err)
				break
			}
			continue
		}
		eventType := strings.TrimSpace(event.Type)
		if !strictV2 {
			eventType = strings.ToLower(eventType)
		}
		item := event.normalizedItem()
		if !strictV2 && eventType == "" && item.hasPayload() {
			eventType = "item"
		}
		if strictV2 && eventType == "item" {
			if err := validateV2Item(item); err != nil {
				result.Error = err.Error()
				break
			}
			v2ItemsSeen++
		}
		if eventType == "done" && strictV2 {
			if err := validateDoneStats(event.Stats, v2ItemsSeen); err != nil {
				result.Error = err.Error()
				break
			}
		}
		if eventType == "progress" && strictV2 && (event.Checked < 0 || event.Emitted < 0) {
			result.Error = "crawler.v2 progress.checked 和 progress.emitted 不能为负数"
			break
		}
		if eventType != "item" {
			if strictV2 && eventType != "progress" && eventType != "done" {
				if eventType == "" {
					result.Error = "crawler.v2 事件缺少 type"
				} else {
					result.Error = fmt.Sprintf("crawler.v2 不支持事件类型 %q", event.Type)
				}
				break
			}
			continue
		}
		normalized, _, err := normalizeItemForImport(item)
		if err != nil {
			result.Error = fmt.Sprintf("item 字段不完整: %v", err)
			continue
		}
		mediaURL := strings.TrimSpace(normalized.Media.URL)
		if len(items) == 0 {
			firstMediaHeaders = normalized.Media.Headers
		}
		items = append(items, DryRunItem{
			Title:          strings.TrimSpace(normalized.Title),
			SourceID:       strings.TrimSpace(item.SourceID),
			MediaURL:       mediaURL,
			MediaLocalFile: strings.TrimSpace(normalized.Media.LocalFile),
			ThumbnailURL:   strings.TrimSpace(normalized.Thumbnail.URL),
			DetailURL:      strings.TrimSpace(normalized.DetailURL),
		})
		if len(items) >= maxItems {
			break
		}
	}
	if scanErr := scanner.Err(); scanErr != nil && result.Error == "" {
		result.Error = fmt.Sprintf("脚本 stdout 单行超过 %d 字节或读取失败: %v", maxLineBytes, scanErr)
	}
	// 拿够了就停掉脚本，避免它继续翻页。给已经自然结束的脚本一个很短
	// 的宽限期，让 stderr 日志先被管道读完，避免 dry-run 回显偶发为空。
	waitDone := make(chan struct{})
	go func() {
		_ = cmd.Wait()
		close(waitDone)
	}()
	select {
	case <-waitDone:
	case <-time.After(dryRunStopGrace):
		_ = killDryRunProcess(cmd)
		<-waitDone
	}

	result.Log = logTail.snapshot()
	result.Items = items

	if len(items) == 0 {
		if result.Error == "" {
			switch {
			case runCtx.Err() != nil && ctx.Err() == nil:
				result.Error = fmt.Sprintf("测试超时（%s），脚本没有输出任何视频", timeout)
			case parseFailures > 0:
				result.Error = fmt.Sprintf("脚本 stdout 不是合法的 %s JSON Lines（日志应输出到 stderr）", metadata.Protocol)
			default:
				result.Error = "脚本退出但没有输出任何视频"
			}
		}
		return result
	}
	result.Error = ""

	first := items[0]
	switch {
	case cfg.SkipMediaProbe:
		result.OK = true
	case first.MediaLocalFile != "":
		// 脚本自己下载到 output_dir 的模式：试跑用的是临时目录，
		// 文件已随目录清理，能输出合法 local_file 即视为通过。
		result.OK = true
	default:
		check := probeMediaURL(ctx, cfg, first, firstMediaHeaders)
		result.MediaCheck = check
		result.OK = check.OK
	}
	return result
}

// probeMediaURL 对视频直链发一个 Range: bytes=0-0 的小请求，
// 验证直链可达（带上脚本给的防盗链 headers 和代理）。
func probeMediaURL(ctx context.Context, cfg DryRunConfig, item DryRunItem, mediaHeaders map[string]string) *DryRunMediaCheck {
	check := &DryRunMediaCheck{}
	if item.MediaURL == "" {
		check.Error = "item 没有视频直链"
		return check
	}

	client := cfg.HTTPClient
	if client == nil {
		transport := &http.Transport{
			Proxy:                 http.ProxyFromEnvironment,
			ResponseHeaderTimeout: dryRunMediaProbeLimit,
		}
		if err := configureExplicitProxy(transport, cfg.ProxyURL); err != nil {
			check.Error = fmt.Sprintf("代理配置无效: %v", err)
			return check
		}
		client = &http.Client{Transport: transport}
	}

	probeCtx, cancel := context.WithTimeout(ctx, dryRunMediaProbeLimit)
	defer cancel()
	req, err := http.NewRequestWithContext(probeCtx, http.MethodGet, item.MediaURL, nil)
	if err != nil {
		check.Error = fmt.Sprintf("视频直链无效: %v", err)
		return check
	}
	req.Header.Set("User-Agent", defaultUserAgent)
	req.Header.Set("Range", "bytes=0-0")
	if item.DetailURL != "" {
		req.Header.Set("Referer", item.DetailURL)
	}
	for k, v := range mediaHeaders {
		k = strings.TrimSpace(k)
		if k == "" {
			continue
		}
		req.Header.Set(k, v)
	}
	resp, err := client.Do(req)
	if err != nil {
		check.Error = fmt.Sprintf("视频直链请求失败: %v", err)
		return check
	}
	defer resp.Body.Close()

	check.Status = resp.StatusCode
	check.ContentType = resp.Header.Get("Content-Type")
	if cr := resp.Header.Get("Content-Range"); cr != "" {
		// Content-Range: bytes 0-0/12345 → 取总大小
		if idx := strings.LastIndex(cr, "/"); idx >= 0 {
			var total int64
			if _, err := fmt.Sscanf(cr[idx+1:], "%d", &total); err == nil {
				check.ContentLength = total
			}
		}
	}
	if check.ContentLength == 0 && resp.StatusCode == http.StatusOK {
		check.ContentLength = resp.ContentLength
	}
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusPartialContent {
		check.Error = fmt.Sprintf("视频直链返回 HTTP %d", resp.StatusCode)
		return check
	}
	check.OK = true
	return check
}
