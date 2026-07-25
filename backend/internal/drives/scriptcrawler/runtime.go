package scriptcrawler

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"os/exec"
	"strings"
	"time"
)

type scriptOutput struct {
	line string
	err  error
}

type doneStats struct {
	Checked *int `json:"checked"`
	Emitted *int `json:"emitted"`
}

func (c *Crawler) executeScript(
	runCtx context.Context,
	parentCtx context.Context,
	jobPath string,
	targetNew int,
	candidateBudget int,
	result *CrawlResult,
	emit func(CrawlProgress),
) error {
	cmd, stdout, err := c.startScript(runCtx, jobPath, targetNew, candidateBudget)
	if err != nil {
		return fmt.Errorf("scriptcrawler: start: %w", err)
	}
	defer stdout.Close()

	maxLineBytes := maxV1StdoutLineBytes
	strictV2 := c.protocol == ProtocolV2
	if strictV2 {
		maxLineBytes = maxV2StdoutLineBytes
	}
	outputCh := scanScriptOutput(runCtx, stdout, maxLineBytes, c.cfg.MaxStdoutBytes)

	runTimeout := c.effectiveRunTimeout()
	candidateIdleTimeout := c.effectiveCandidateIdleTimeout()
	var candidateTimer *time.Timer
	var candidateC <-chan time.Time
	if candidateIdleTimeout > 0 {
		candidateTimer = time.NewTimer(candidateIdleTimeout)
		candidateC = candidateTimer.C
		defer candidateTimer.Stop()
	}
	var idleTimer *time.Timer
	var idleC <-chan time.Time
	if strictV2 {
		idleTimer = time.NewTimer(c.cfg.IdleTimeout)
		idleC = idleTimer.C
		defer idleTimer.Stop()
	}
	var doneTimer *time.Timer
	var doneC <-chan time.Time
	defer func() {
		if doneTimer != nil {
			doneTimer.Stop()
		}
	}()

	progress := CrawlProgress{}
	doneSeen := false
	for {
		select {
		case <-runCtx.Done():
			c.stopScript(cmd, stdout)
			if err := parentCtx.Err(); err != nil {
				return err
			}
			return fmt.Errorf("scriptcrawler: maximum runtime exceeded (%s)", runTimeout)

		case <-candidateC:
			c.stopScript(cmd, stdout)
			return fmt.Errorf("scriptcrawler: no item event received for %s", candidateIdleTimeout)

		case <-idleC:
			c.stopScript(cmd, stdout)
			return fmt.Errorf("scriptcrawler: %s heartbeat timeout: no item/progress/done event for %s", ProtocolV2, c.cfg.IdleTimeout)

		case <-doneC:
			c.stopScript(cmd, stdout)
			return fmt.Errorf("scriptcrawler: %s emitted done but did not exit within %s", ProtocolV2, c.cfg.DoneGrace)

		case output, ok := <-outputCh:
			if !ok {
				waitErr := waitForScript(cmd, stdout)
				if err := parentCtx.Err(); err != nil {
					return err
				}
				if runCtx.Err() != nil {
					return fmt.Errorf("scriptcrawler: maximum runtime exceeded (%s)", runTimeout)
				}
				if waitErr != nil {
					return fmt.Errorf("scriptcrawler: script exited unsuccessfully: %w", waitErr)
				}
				if strictV2 && !doneSeen {
					return fmt.Errorf("scriptcrawler: %s script exited without a done event", ProtocolV2)
				}
				return nil
			}
			if output.err != nil {
				c.stopScript(cmd, stdout)
				return fmt.Errorf("scriptcrawler: stdout protocol violation: %w", output.err)
			}

			line := strings.TrimSpace(output.line)
			if line == "" {
				if strictV2 {
					c.stopScript(cmd, stdout)
					return errors.New("scriptcrawler: crawler.v2 stdout contains a blank line")
				}
				continue
			}
			if strictV2 && doneSeen {
				c.stopScript(cmd, stdout)
				return errors.New("scriptcrawler: crawler.v2 emitted output after the terminal done event")
			}

			var event Event
			if err := json.Unmarshal([]byte(line), &event); err != nil {
				if strictV2 {
					c.stopScript(cmd, stdout)
					return fmt.Errorf("scriptcrawler: crawler.v2 stdout must contain JSON objects only: %w", err)
				}
				log.Printf("[scriptcrawler] drive=%s stdout parse: %v line=%q", c.cfg.Driver.ID(), err, truncateLogValue(line))
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
			switch eventType {
			case "item":
				if strictV2 {
					if err := validateV2Item(item); err != nil {
						c.stopScript(cmd, stdout)
						return err
					}
					stopTimer(idleTimer)
				}
				// Downloading, fingerprinting and ingesting an item are backend work.
				// Pause script-silence timers so a slow media download is not blamed on
				// the crawler process.
				stopTimer(candidateTimer)
				result.TotalEntries++
				progress.Emitted++
				emit(progress)

				added, itemErr := c.processItem(runCtx, item)
				if runCtx.Err() != nil {
					c.stopScript(cmd, stdout)
					if err := parentCtx.Err(); err != nil {
						return err
					}
					return fmt.Errorf("scriptcrawler: maximum runtime exceeded (%s)", runTimeout)
				}
				if itemErr != nil {
					log.Printf("[scriptcrawler] drive=%s item failed source_id=%q title=%q: %v", c.cfg.Driver.ID(), item.SourceID, item.Title, itemErr)
					result.Failed++
				} else if added {
					result.NewVideos++
				} else {
					result.Skipped++
				}
				emit(progress)
				resetTimer(candidateTimer, candidateIdleTimeout)
				if strictV2 {
					resetTimer(idleTimer, c.cfg.IdleTimeout)
				}

				if result.NewVideos >= targetNew || result.TotalEntries >= candidateBudget {
					c.stopScript(cmd, stdout)
					return nil
				}

			case "progress":
				if strictV2 {
					if event.Checked < 0 || event.Emitted < 0 {
						c.stopScript(cmd, stdout)
						return errors.New("scriptcrawler: crawler.v2 progress.checked and progress.emitted cannot be negative")
					}
					resetTimer(idleTimer, c.cfg.IdleTimeout)
				}
				if event.Checked > 0 {
					progress.Checked = event.Checked
				}
				if event.Emitted > 0 {
					progress.Emitted = event.Emitted
				}
				progress.Message = event.Message
				emit(progress)

			case "done":
				if strictV2 {
					if err := validateDoneStats(event.Stats, result.TotalEntries); err != nil {
						c.stopScript(cmd, stdout)
						return err
					}
					stopTimer(candidateTimer)
					idleC = nil
					stopTimer(idleTimer)
					doneSeen = true
					doneTimer = time.NewTimer(c.cfg.DoneGrace)
					doneC = doneTimer.C
				}
				progress.Message = event.Message
				emit(progress)

			case "":
				if strictV2 {
					c.stopScript(cmd, stdout)
					return errors.New("scriptcrawler: crawler.v2 event is missing type")
				}
				log.Printf("[scriptcrawler] drive=%s missing event type line=%q", c.cfg.Driver.ID(), truncateLogValue(line))

			default:
				if strictV2 {
					c.stopScript(cmd, stdout)
					return fmt.Errorf("scriptcrawler: crawler.v2 unknown event type %q", event.Type)
				}
				log.Printf("[scriptcrawler] drive=%s unknown event type=%q", c.cfg.Driver.ID(), event.Type)
			}
		}
	}
}

func scanScriptOutput(ctx context.Context, r io.Reader, maxLineBytes int, maxTotalBytes int64) <-chan scriptOutput {
	out := make(chan scriptOutput, 4)
	go func() {
		defer close(out)
		scanner := bufio.NewScanner(r)
		initialBufferBytes := 64 * 1024
		if maxLineBytes < initialBufferBytes {
			initialBufferBytes = maxLineBytes
		}
		scanner.Buffer(make([]byte, initialBufferBytes), maxLineBytes)
		var total int64
		for scanner.Scan() {
			line := scanner.Text()
			total += int64(len(line)) + 1
			if total > maxTotalBytes {
				sendScriptOutput(ctx, out, scriptOutput{err: fmt.Errorf("stdout exceeded %d bytes", maxTotalBytes)})
				return
			}
			if !sendScriptOutput(ctx, out, scriptOutput{line: line}) {
				return
			}
		}
		if err := scanner.Err(); err != nil {
			sendScriptOutput(ctx, out, scriptOutput{err: fmt.Errorf("stdout line exceeds %d bytes or cannot be read: %w", maxLineBytes, err)})
		}
	}()
	return out
}

func sendScriptOutput(ctx context.Context, out chan<- scriptOutput, value scriptOutput) bool {
	select {
	case out <- value:
		return true
	case <-ctx.Done():
		return false
	}
}

func validateV2Item(item Item) error {
	if strings.TrimSpace(item.SourceID) == "" {
		return errors.New("scriptcrawler: crawler.v2 item.source_id is required")
	}
	if normalizeSourceID(item.SourceID) == "" {
		return errors.New("scriptcrawler: crawler.v2 item.source_id must contain at least one ASCII letter or digit")
	}
	if strings.TrimSpace(item.Title) == "" {
		return errors.New("scriptcrawler: crawler.v2 item.title is required")
	}
	if strings.TrimSpace(item.MediaURL) == "" &&
		strings.TrimSpace(item.MediaLocalFile) == "" &&
		strings.TrimSpace(item.Media.URL) == "" &&
		strings.TrimSpace(item.Media.LocalFile) == "" {
		return errors.New("scriptcrawler: crawler.v2 item.media_url or item.media_local_file is required")
	}
	return nil
}

func validateDoneStats(raw json.RawMessage, expectedEmitted int) error {
	if len(raw) == 0 || string(raw) == "null" {
		return errors.New("scriptcrawler: crawler.v2 done.stats is required")
	}
	var stats doneStats
	if err := json.Unmarshal(raw, &stats); err != nil {
		return fmt.Errorf("scriptcrawler: crawler.v2 done.stats must be an object: %w", err)
	}
	if stats.Checked == nil || stats.Emitted == nil {
		return errors.New("scriptcrawler: crawler.v2 done.stats.checked and done.stats.emitted are required")
	}
	if *stats.Checked < 0 || *stats.Emitted < 0 {
		return errors.New("scriptcrawler: crawler.v2 done.stats values cannot be negative")
	}
	if *stats.Checked < *stats.Emitted {
		return errors.New("scriptcrawler: crawler.v2 done.stats.checked cannot be less than done.stats.emitted")
	}
	if *stats.Emitted != expectedEmitted {
		return fmt.Errorf("scriptcrawler: crawler.v2 done.stats.emitted=%d does not match %d item events", *stats.Emitted, expectedEmitted)
	}
	return nil
}

func (c *Crawler) stopScript(cmd *exec.Cmd, stdout io.Closer) {
	terminated := killCrawlerProcess(cmd) == nil
	_ = stdout.Close()
	if err := cmd.Wait(); err != nil && !isExpectedKilledProcess(err, terminated) {
		log.Printf("[scriptcrawler] drive=%s wait after stop: %v", c.cfg.Driver.ID(), err)
	}
}

func waitForScript(cmd *exec.Cmd, stdout io.Closer) error {
	_ = stdout.Close()
	return cmd.Wait()
}

func resetTimer(timer *time.Timer, duration time.Duration) {
	if timer == nil {
		return
	}
	if !timer.Stop() {
		select {
		case <-timer.C:
		default:
		}
	}
	timer.Reset(duration)
}

func stopTimer(timer *time.Timer) {
	if timer == nil {
		return
	}
	if !timer.Stop() {
		select {
		case <-timer.C:
		default:
		}
	}
}

func truncateLogValue(value string) string {
	const max = 1024
	if len(value) <= max {
		return value
	}
	return value[:max] + "…"
}
