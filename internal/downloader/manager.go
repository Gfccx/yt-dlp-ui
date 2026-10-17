package downloader

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/Scrin/yt-dlp-ui/internal/network"
	"github.com/google/uuid"
)

// JobStatus represents the state of a download job.
type JobStatus string

const (
	StatusQueued      JobStatus = "queued"
	StatusDownloading JobStatus = "downloading"
	StatusProcessing  JobStatus = "processing"
	StatusComplete    JobStatus = "complete"
	StatusError       JobStatus = "error"
	StatusCancelled   JobStatus = "cancelled"
)

// jobTTL is how long finished jobs are retained before being evicted.
const jobTTL = time.Hour

// JobProgress holds current download progress.
type JobProgress struct {
	Percent  float64 `json:"percent"`
	Speed    float64 `json:"speed"`
	ETA      float64 `json:"eta"`
	Elapsed  float64 `json:"elapsed"`
	FileSize int64   `json:"file_size"`
}

// Job represents a download task.
type Job struct {
	ID               string         `json:"id"`
	URL              string         `json:"url"`
	Title            string         `json:"title"`
	FormatID         string         `json:"format_id"`
	Status           JobStatus      `json:"status"`
	Progress         JobProgress    `json:"progress"`
	Filename         string         `json:"filename"`
	Error            string         `json:"error,omitempty"`
	CreatedAt        time.Time      `json:"created_at"`
	FinishedAt       time.Time      `json:"finished_at,omitempty"`
	QualityTag       string         `json:"quality_tag,omitempty"`
	PlaylistTitle    string         `json:"playlist_title,omitempty"`
	ProxyMode        string         `json:"proxy_mode"`
	ProxyLabel       string         `json:"proxy_label"`
	Audio            AudioSelection `json:"audio"`
	AudioLanguage    string         `json:"audio_language,omitempty"`
	AudioFormatID    string         `json:"audio_format_id,omitempty"`
	ResolvedFormatID string         `json:"resolved_format_id,omitempty"`

	cancel   context.CancelFunc
	proxyURL string
}

// normalizeCodec collapses verbose yt-dlp codec strings (e.g. "avc1.640028",
// "mp4a.40.2", "vp09.00.41.08") into short, human-readable names for
// filenames. Unrecognized inputs fall back to the portion before the first
// dot, lowercased. Empty / "none" / "NA" → "" (caller treats as absent).
func normalizeCodec(c string) string {
	c = strings.ToLower(strings.TrimSpace(c))
	if c == "" || c == "none" || c == "na" {
		return ""
	}
	head := c
	if i := strings.IndexByte(head, '.'); i > 0 {
		head = head[:i]
	}
	switch head {
	case "avc1", "avc3":
		return "h264"
	case "hev1", "hvc1":
		return "h265"
	case "vp09":
		return "vp9"
	case "mp4a":
		return "aac"
	case "ac-3":
		return "ac3"
	case "ec-3":
		return "eac3"
	}
	return head
}

// buildQualityTag computes a filename-safe quality/codec string from format
// metadata reported by yt-dlp after download/merge. For video formats it uses
// height and normalized vcodec; for audio-only it uses abr and normalized
// acodec.
func buildQualityTag(height int, abr float64, vcodec, acodec string) string {
	sanitize := func(s string) string {
		return strings.Map(func(r rune) rune {
			if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') ||
				(r >= '0' && r <= '9') || r == '-' {
				return r
			}
			return '_'
		}, s)
	}
	vc := normalizeCodec(vcodec)
	ac := normalizeCodec(acodec)
	if height == 0 && vc == "" {
		codec := ac
		if codec == "" {
			codec = "audio"
		}
		return fmt.Sprintf("%.0fkbps_%s", abr, sanitize(codec))
	}
	if vc == "" {
		vc = "video"
	}
	tag := fmt.Sprintf("%dp_%s", height, sanitize(vc))
	if ac != "" {
		tag += "_" + sanitize(ac)
	}
	return tag
}

// JobEvent is sent to SSE subscribers when job state changes.
type JobEvent struct {
	Type string `json:"type"` // job:created, job:progress, job:complete, job:error, job:cancelled
	Job  *Job   `json:"job"`
}

// EventSubscriber receives job events.
type EventSubscriber func(event JobEvent)

// Manager manages download jobs.
type Manager struct {
	mu          sync.RWMutex
	jobs        map[string]*Job
	queue       chan *Job
	ytdlp       *YtDlp
	downloadDir string

	subMu       sync.RWMutex
	subscribers map[string]chan JobEvent
}

// NewManager creates a new job manager.
func NewManager(ytdlp *YtDlp, downloadDir string, maxConcurrent int) *Manager {
	m := &Manager{
		jobs: make(map[string]*Job),
		// Buffer sized to comfortably hold a queued playlist (thousands of jobs)
		// so Submit() never blocks the HTTP handler waiting for workers.
		queue:       make(chan *Job, 10000),
		ytdlp:       ytdlp,
		downloadDir: downloadDir,
		subscribers: make(map[string]chan JobEvent),
	}

	// Start worker pool
	for i := 0; i < maxConcurrent; i++ {
		go m.worker()
	}

	// Start background cleanup of finished jobs
	go m.cleanupLoop()

	return m
}

// Submit creates a new download job and enqueues it.
// playlistTitle is optional; when set, the job is part of a playlist batch and
// the UI groups sibling jobs together. QualityTag is populated after the
// download completes, from metadata yt-dlp reports for the resolved format.
func (m *Manager) Submit(url, formatID, title, playlistTitle string, selections ...network.Selection) *Job {
	selection := network.Selection{Mode: "global"}
	if len(selections) > 0 {
		selection = selections[0]
	}
	return m.submit(url, formatID, title, playlistTitle, selection, AudioSelection{}, "")
}

func (m *Manager) SubmitWithAudio(url, formatID, title, playlistTitle string, selection network.Selection, audio AudioSelection) *Job {
	return m.submit(url, formatID, title, playlistTitle, selection, audio, "")
}

func (m *Manager) submit(url, formatID, title, playlistTitle string, selection network.Selection, audio AudioSelection, pinned string) *Job {
	job := &Job{
		ID:            uuid.New().String(),
		URL:           url,
		Title:         title,
		FormatID:      formatID,
		Status:        StatusQueued,
		CreatedAt:     time.Now(),
		PlaylistTitle: playlistTitle,
		ProxyMode:     selection.Mode,
		ProxyLabel:    network.Label(selection.URL),
		proxyURL:      selection.URL,
		Audio:         audio,
		AudioFormatID: pinned,
	}

	snapshot := *job
	m.mu.Lock()
	m.jobs[job.ID] = job
	m.mu.Unlock()

	m.broadcast(JobEvent{Type: "job:created", Job: job})
	m.queue <- job

	return &snapshot
}

// Retry creates a new job from a terminal job, retaining the original network
// snapshot and audio policy, plus a previously resolved playlist audio ID.
func (m *Manager) Retry(id string) (*Job, error) {
	job, ok := m.Get(id)
	if !ok {
		return nil, fmt.Errorf("job not found: %s", id)
	}
	if job.Status != StatusError && job.Status != StatusCancelled {
		return nil, fmt.Errorf("only failed or cancelled jobs can be retried")
	}
	return m.submit(job.URL, job.FormatID, job.Title, job.PlaylistTitle, network.Selection{Mode: job.ProxyMode, URL: job.proxyURL}, job.Audio, job.AudioFormatID), nil
}

// Cancel cancels a running or queued job.
func (m *Manager) Cancel(id string) error {
	m.mu.Lock()
	job, ok := m.jobs[id]
	if !ok {
		m.mu.Unlock()
		return fmt.Errorf("job not found: %s", id)
	}
	if job.Status != StatusQueued && job.Status != StatusDownloading && job.Status != StatusProcessing {
		m.mu.Unlock()
		return fmt.Errorf("job %s is not cancellable (status: %s)", id, job.Status)
	}
	job.Status = StatusCancelled
	job.FinishedAt = time.Now()
	if job.cancel != nil {
		job.cancel()
	}
	m.mu.Unlock()

	m.broadcast(JobEvent{Type: "job:cancelled", Job: job})
	return nil
}

// RemoveByFilename removes the job whose Filename matches and broadcasts a job:removed event.
// It is a no-op if no matching job exists.
func (m *Manager) RemoveByFilename(filename string) {
	m.mu.Lock()
	var removed *Job
	for id, job := range m.jobs {
		if job.Filename == filename {
			removed = job
			delete(m.jobs, id)
			break
		}
	}
	m.mu.Unlock()

	if removed != nil {
		m.broadcast(JobEvent{Type: "job:removed", Job: removed})
	}
}

// List returns all jobs.
func (m *Manager) List() []*Job {
	m.mu.RLock()
	defer m.mu.RUnlock()

	jobs := make([]*Job, 0, len(m.jobs))
	for _, j := range m.jobs {
		snapshot := *j
		jobs = append(jobs, &snapshot)
	}
	return jobs
}

// Get returns a single job by ID.
func (m *Manager) Get(id string) (*Job, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	j, ok := m.jobs[id]
	if !ok {
		return nil, false
	}
	snapshot := *j
	return &snapshot, true
}

// Subscribe creates a new event subscription. Returns the channel and an unsubscribe function.
func (m *Manager) Subscribe(id string) (<-chan JobEvent, func()) {
	ch := make(chan JobEvent, 64)

	m.subMu.Lock()
	m.subscribers[id] = ch
	m.subMu.Unlock()

	unsub := func() {
		m.subMu.Lock()
		delete(m.subscribers, id)
		close(ch)
		m.subMu.Unlock()
	}

	return ch, unsub
}

func (m *Manager) cleanupLoop() {
	ticker := time.NewTicker(5 * time.Minute)
	defer ticker.Stop()
	for range ticker.C {
		cutoff := time.Now().Add(-jobTTL)
		m.mu.Lock()
		for id, job := range m.jobs {
			if !job.FinishedAt.IsZero() && job.FinishedAt.Before(cutoff) {
				delete(m.jobs, id)
			}
		}
		m.mu.Unlock()
	}
}

func (m *Manager) broadcast(event JobEvent) {
	m.mu.RLock()
	snapshot := *event.Job
	m.mu.RUnlock()
	event.Job = &snapshot
	m.subMu.RLock()
	defer m.subMu.RUnlock()

	for _, ch := range m.subscribers {
		select {
		case ch <- event:
		default:
			// Subscriber too slow, drop event
		}
	}
}

func (m *Manager) worker() {
	for job := range m.queue {
		m.mu.RLock()
		if job.Status == StatusCancelled {
			m.mu.RUnlock()
			continue
		}
		m.mu.RUnlock()

		m.processJob(job)
	}
}

func (m *Manager) processJob(job *Job) {
	ctx, cancel := context.WithCancel(context.Background())

	m.mu.Lock()
	if job.Status == StatusCancelled {
		m.mu.Unlock()
		cancel()
		return
	}
	job.cancel = cancel
	job.Status = StatusDownloading
	m.mu.Unlock()

	m.broadcast(JobEvent{Type: "job:progress", Job: job})

	progressCh := make(chan Progress, 32)

	// Read progress updates in a goroutine
	done := make(chan struct{})
	go func() {
		defer close(done)
		for p := range progressCh {
			m.mu.Lock()
			if job.Status == StatusCancelled {
				m.mu.Unlock()
				continue
			}
			if p.Status == "downloading" {
				job.Status = StatusDownloading
				total := p.TotalBytes
				if total == 0 {
					total = p.TotalEstimate
				}
				if p.Percent > 0 {
					job.Progress.Percent = p.Percent
				} else if total > 0 && p.DownloadedBytes > 0 {
					job.Progress.Percent = float64(p.DownloadedBytes) / float64(total) * 100
				}
				job.Progress.Speed = p.Speed
				job.Progress.ETA = p.ETA
				job.Progress.Elapsed = p.Elapsed
				job.Progress.FileSize = total
			} else if p.Status == "finished" || p.Status == "processing" || p.Status == "started" {
				job.Status = StatusProcessing
				job.Progress.Percent = 100
			}
			m.mu.Unlock()

			m.broadcast(JobEvent{Type: "job:progress", Job: job})
		}
	}()

	selector := job.FormatID
	var err error
	if job.Audio.Language != "" || job.Audio.FormatID != "" {
		resolveCtx, stop := context.WithTimeout(ctx, 60*time.Second)
		info, resolveErr := m.ytdlp.Resolve(resolveCtx, job.URL, "video", job.proxyURL)
		stop()
		err = resolveErr
		if err == nil {
			if info.Video == nil {
				err = fmt.Errorf("expected video audio formats, received a playlist")
			} else {
				var language, audioID string
				selector, language, audioID, err = selectAudio(*info.Video, job.FormatID, job.Audio, job.AudioFormatID)
				if err == nil {
					m.mu.Lock()
					job.ResolvedFormatID, job.AudioLanguage, job.AudioFormatID = selector, language, audioID
					m.mu.Unlock()
					m.broadcast(JobEvent{Type: "job:progress", Job: job})
				}
			}
		}
	}
	var result *DownloadResult
	if err == nil {
		result, err = m.ytdlp.download(ctx, job.URL, selector, m.downloadDir, progressCh, job.proxyURL, job.AudioFormatID, job.AudioLanguage)
	} else {
		close(progressCh)
	}
	<-done // Wait for progress reader to finish

	m.mu.Lock()
	if job.Status == StatusCancelled {
		m.mu.Unlock()
		cancel()
		return
	}

	if err != nil {
		job.Status = StatusError
		job.Error = err.Error()
		job.FinishedAt = time.Now()
		m.mu.Unlock()
		m.broadcast(JobEvent{Type: "job:error", Job: job})
		cancel()
		return
	}

	if result != nil {
		// Explicit selection is authoritative; merged formats may report language
		// only on their requested audio stream. Auto downloads use that metadata too.
		job.AudioLanguage = resolvedAudioLanguage(*result, job.AudioLanguage)
		tag := buildQualityTag(result.Height, result.ABR, result.VCodec, result.ACodec)
		if job.AudioLanguage != "" {
			tag += "_" + job.AudioLanguage
		}
		job.QualityTag = tag
		finalPath, renameErr := renameWithQualityTag(*result, tag)
		if renameErr != nil {
			job.Status = StatusError
			job.Error = renameErr.Error()
			job.FinishedAt = time.Now()
			m.mu.Unlock()
			m.broadcast(JobEvent{Type: "job:error", Job: job})
			cancel()
			return
		}
		job.Filename = filepath.Base(finalPath)
	}
	job.Status = StatusComplete
	job.Progress.Percent = 100
	job.FinishedAt = time.Now()
	m.mu.Unlock()

	m.broadcast(JobEvent{Type: "job:complete", Job: job})
	cancel()
}
