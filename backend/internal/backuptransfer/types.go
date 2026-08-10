package backuptransfer

import (
	"errors"
	"time"

	"github.com/video-site/backend/internal/backup"
)

const (
	ProtocolVersion = 1

	TransferQueued     = "queued"
	TransferConnecting = "connecting"
	TransferUploading  = "uploading"
	TransferFinalizing = "finalizing"
	TransferRetrying   = "retrying"
	TransferCompleted  = "completed"
	TransferFailed     = "failed"
	TransferCanceled   = "canceled"

	ImportUploading  = "uploading"
	ImportFinalizing = "finalizing"
	ImportCompleted  = "completed"
	ImportCanceled   = "canceled"
)

const (
	defaultPairingTTL   = 10 * time.Minute
	boundTokenTTL       = 72 * time.Hour
	completedReceiptTTL = 30 * 24 * time.Hour
	finishedJobTTL      = 30 * 24 * time.Hour
)

var (
	ErrUnavailable          = errors.New("服务器直传服务未配置")
	ErrUnauthorized         = errors.New("接收码无效或已过期")
	ErrTokenBound           = errors.New("接收码已被其它传输使用")
	ErrImportNotFound       = errors.New("服务器传输会话不存在")
	ErrImportConflict       = errors.New("相同传输编号的备份元数据不一致")
	ErrTransferNotFound     = errors.New("服务器发送任务不存在")
	ErrTransferBusy         = errors.New("已有备份正在发送，请等待当前任务结束")
	ErrTransferTerminal     = errors.New("服务器发送任务已经结束")
	ErrTransferNotRetryable = errors.New("服务器发送任务当前不可重试")
)

type Capabilities struct {
	ProtocolVersion      int   `json:"protocolVersion"`
	BackupFormatVersions []int `json:"backupFormatVersions"`
	ChunkSize            int64 `json:"chunkSize"`
}

type ReceiveToken struct {
	ID        string    `json:"id"`
	Token     string    `json:"token"`
	CreatedAt time.Time `json:"createdAt"`
	ExpiresAt time.Time `json:"expiresAt"`
}

type ReceiveTokenInfo struct {
	ID              string    `json:"id"`
	CreatedAt       time.Time `json:"createdAt"`
	ExpiresAt       time.Time `json:"expiresAt"`
	LastUsedAt      time.Time `json:"lastUsedAt,omitempty"`
	BoundTransferID string    `json:"boundTransferId,omitempty"`
	Revoked         bool      `json:"revoked"`
}

type CreateTransferInput struct {
	BackupID     string `json:"backupId"`
	TargetURL    string `json:"targetUrl"`
	ReceiveToken string `json:"receiveToken"`
}

type TransferJob struct {
	ID               string    `json:"id"`
	BackupID         string    `json:"backupId"`
	BackupName       string    `json:"backupName"`
	TargetURL        string    `json:"targetUrl"`
	State            string    `json:"state"`
	Size             int64     `json:"size"`
	SHA256           string    `json:"sha256"`
	ProcessedBytes   int64     `json:"processedBytes"`
	TotalChunks      int       `json:"totalChunks,omitempty"`
	ProcessedChunks  int       `json:"processedChunks,omitempty"`
	Attempts         int       `json:"attempts,omitempty"`
	NextAttemptAt    time.Time `json:"nextAttemptAt,omitempty"`
	Error            string    `json:"error,omitempty"`
	TargetBackupID   string    `json:"targetBackupId,omitempty"`
	TargetBackupName string    `json:"targetBackupName,omitempty"`
	CreatedAt        time.Time `json:"createdAt"`
	StartedAt        time.Time `json:"startedAt,omitempty"`
	UpdatedAt        time.Time `json:"updatedAt"`
	FinishedAt       time.Time `json:"finishedAt,omitempty"`
	Cancellable      bool      `json:"cancellable"`
	Retryable        bool      `json:"retryable"`
}

type ImportRequest struct {
	TransferID     string `json:"transferId"`
	SourceServerID string `json:"sourceServerId"`
	BackupID       string `json:"backupId"`
	FileName       string `json:"fileName"`
	Size           int64  `json:"size"`
	SHA256         string `json:"sha256"`
	FormatVersion  int    `json:"formatVersion"`
}

type ChunkRange struct {
	Start int `json:"start"`
	End   int `json:"end"`
}

type ImportStatus struct {
	TransferID    string               `json:"transferId"`
	State         string               `json:"state"`
	Size          int64                `json:"size"`
	SHA256        string               `json:"sha256"`
	ChunkSize     int64                `json:"chunkSize"`
	TotalChunks   int                  `json:"totalChunks"`
	Received      []ChunkRange         `json:"received"`
	ReceivedBytes int64                `json:"receivedBytes"`
	ExpiresAt     time.Time            `json:"expiresAt,omitempty"`
	Record        *backup.BackupRecord `json:"record,omitempty"`
}

func (j TransferJob) terminal() bool {
	switch j.State {
	case TransferCompleted, TransferFailed, TransferCanceled:
		return true
	default:
		return false
	}
}
