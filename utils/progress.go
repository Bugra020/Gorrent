package utils

import (
	"fmt"
	"strings"
	"sync"
	"time"
)

type ProgressTracker struct {
	mu              sync.RWMutex
	totalPieces     int
	downloaded      int
	inProgress      int
	totalSize       int64
	downloadedBytes int64
	startTime       time.Time
	lastUpdate      time.Time
	speeds          []float64
	maxSpeeds       int
}

func NewProgressTracker(totalPieces int, totalSize int64) *ProgressTracker {
	return &ProgressTracker{
		totalPieces: totalPieces,
		totalSize:   totalSize,
		startTime:   time.Now(),
		lastUpdate:  time.Now(),
		maxSpeeds:   10,
		speeds:      make([]float64, 0, 10),
	}
}

func (p *ProgressTracker) UpdateProgress(downloaded, inProgress int, downloadedBytes int64) {
	p.mu.Lock()
	defer p.mu.Unlock()

	now := time.Now()

	if !p.lastUpdate.IsZero() {
		timeDiff := now.Sub(p.lastUpdate).Seconds()
		if timeDiff > 0 {
			bytesDiff := downloadedBytes - p.downloadedBytes
			speed := float64(bytesDiff) / timeDiff

			p.speeds = append(p.speeds, speed)
			if len(p.speeds) > p.maxSpeeds {
				p.speeds = p.speeds[1:]
			}
		}
	}

	p.downloaded = downloaded
	p.inProgress = inProgress
	p.downloadedBytes = downloadedBytes
	p.lastUpdate = now
}

func (p *ProgressTracker) GetAverageSpeed() float64 {
	p.mu.RLock()
	defer p.mu.RUnlock()

	if len(p.speeds) == 0 {
		return 0
	}

	var sum float64
	for _, speed := range p.speeds {
		sum += speed
	}
	return sum / float64(len(p.speeds))
}

func (p *ProgressTracker) GetProgress() (float64, string) {
	p.mu.RLock()
	defer p.mu.RUnlock()

	progress := float64(p.downloaded) / float64(p.totalPieces)

	barWidth := 40
	filledWidth := int(progress * float64(barWidth))
	emptyWidth := barWidth - filledWidth

	bar := "[" + strings.Repeat("=", filledWidth) + strings.Repeat("-", emptyWidth) + "]"

	avgSpeed := p.GetAverageSpeed()
	elapsed := time.Since(p.startTime)

	var eta string
	if avgSpeed > 0 && p.downloadedBytes < p.totalSize {
		remainingBytes := p.totalSize - p.downloadedBytes
		etaSeconds := float64(remainingBytes) / avgSpeed
		eta = fmt.Sprintf(" ETA: %s", time.Duration(etaSeconds*float64(time.Second)).Round(time.Second))
	}

	status := fmt.Sprintf("%s %.1f%% (%d/%d pieces) %s/s%s | Elapsed: %s",
		bar,
		progress*100,
		p.downloaded,
		p.totalPieces,
		formatBytes(int64(avgSpeed)),
		eta,
		elapsed.Round(time.Second),
	)

	if p.inProgress > 0 {
		status += fmt.Sprintf(" | In Progress: %d", p.inProgress)
	}

	return progress, status
}

func (p *ProgressTracker) IsComplete() bool {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.downloaded == p.totalPieces
}

func formatBytes(bytes int64) string {
	const unit = 1024
	if bytes < unit {
		return fmt.Sprintf("%d B", bytes)
	}
	div, exp := int64(unit), 0
	for n := bytes / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %cB", float64(bytes)/float64(div), "KMGTPE"[exp])
}
