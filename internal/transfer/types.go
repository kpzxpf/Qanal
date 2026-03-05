package transfer

// ProgressEvent is emitted during send/receive operations.
type ProgressEvent struct {
	Done       int   `json:"done"`
	Total      int   `json:"total"`
	BytesDone  int64 `json:"bytesDone"`
	TotalBytes int64 `json:"totalBytes"`
	SpeedBPS   int64 `json:"speedBps"`
}

// ProgressFn is called after each chunk completes.
// Pass nil to suppress progress notifications.
type ProgressFn func(ProgressEvent)

// SendResult contains the transfer credentials after a successful upload.
type SendResult struct {
	Code string `json:"code"`
	Key  string `json:"key"`
}
