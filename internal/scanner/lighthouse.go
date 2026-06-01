package scanner

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"runtime"
	"time"
)

type LighthouseRunner interface {
	Audit(ctx context.Context, pageURL string) (ScoreSet, error)
}

type CLILighthouseRunner struct {
	Timeout time.Duration
}

func NewCLILighthouseRunner(timeout time.Duration) CLILighthouseRunner {
	return CLILighthouseRunner{Timeout: timeout}
}

func (r CLILighthouseRunner) Audit(ctx context.Context, pageURL string) (ScoreSet, error) {
	if r.Timeout <= 0 {
		r.Timeout = 90 * time.Second
	}
	ctx, cancel := context.WithTimeout(ctx, r.Timeout)
	defer cancel()

	binary := "npx"
	if runtime.GOOS == "windows" {
		binary = "npx.cmd"
	}
	cmd := exec.CommandContext(ctx, binary,
		"lighthouse",
		pageURL,
		"--output=json",
		"--quiet",
		"--chrome-flags=--headless=new --no-sandbox",
	)
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		if ctx.Err() != nil {
			return ScoreSet{}, ctx.Err()
		}
		return ScoreSet{}, fmt.Errorf("lighthouse failed: %w: %s", err, stderr.String())
	}
	return ParseLighthouseScores(stdout.Bytes())
}

func ParseLighthouseScores(body []byte) (ScoreSet, error) {
	var report struct {
		Categories map[string]struct {
			Score *float64 `json:"score"`
		} `json:"categories"`
	}
	if err := json.Unmarshal(body, &report); err != nil {
		return ScoreSet{}, err
	}

	score := ScoreSet{
		Performance:   scoreFrom(report.Categories["performance"].Score),
		Accessibility: scoreFrom(report.Categories["accessibility"].Score),
		BestPractices: scoreFrom(report.Categories["best-practices"].Score),
		SEO:           scoreFrom(report.Categories["seo"].Score),
	}
	score.Health = averagePointers(score.Performance, score.Accessibility, score.BestPractices, score.SEO)
	return score, nil
}

func scoreFrom(value *float64) *float64 {
	if value == nil {
		return nil
	}
	score := *value * 100
	return &score
}

func averagePointers(values ...*float64) *float64 {
	var total float64
	var count int
	for _, value := range values {
		if value == nil {
			continue
		}
		total += *value
		count++
	}
	if count == 0 {
		return nil
	}
	average := total / float64(count)
	return &average
}

type NoopLighthouseRunner struct{}

func (NoopLighthouseRunner) Audit(context.Context, string) (ScoreSet, error) {
	return ScoreSet{}, nil
}
