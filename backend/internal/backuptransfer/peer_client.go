package backuptransfer

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"
)

const maxPeerResponseBytes int64 = 2 << 20

type peerHTTPError struct {
	Status  int
	Message string
}

func (e *peerHTTPError) Error() string {
	if e == nil {
		return "服务器直传请求失败"
	}
	if e.Message != "" {
		return e.Message
	}
	return "目标服务器返回 HTTP " + strconv.Itoa(e.Status)
}

func (e *peerHTTPError) temporary() bool {
	if e == nil {
		return false
	}
	return e.Status == http.StatusRequestTimeout || e.Status == http.StatusTooManyRequests || e.Status >= 500
}

func newPeerHTTPClient() *http.Client {
	dialer := &net.Dialer{Timeout: 30 * time.Second, KeepAlive: 30 * time.Second}
	transport := &http.Transport{
		Proxy:                 nil,
		DialContext:           dialer.DialContext,
		ForceAttemptHTTP2:     false,
		DisableCompression:    true,
		MaxIdleConns:          16,
		MaxIdleConnsPerHost:   ParallelStreams,
		MaxConnsPerHost:       ParallelStreams,
		IdleConnTimeout:       90 * time.Second,
		TLSHandshakeTimeout:   30 * time.Second,
		ResponseHeaderTimeout: 60 * time.Second,
		TLSClientConfig:       &tls.Config{MinVersion: tls.VersionTLS12},
	}
	return &http.Client{
		Transport: transport,
		CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
}

func (m *Manager) peerCapabilities(ctx context.Context, targetURL, token string) (Capabilities, error) {
	var capabilities Capabilities
	err := m.peerJSON(ctx, http.MethodGet, targetURL+PeerBackupPath+"/capabilities", token, nil, &capabilities)
	return capabilities, err
}

func (m *Manager) peerBeginImport(
	ctx context.Context,
	targetURL, token string,
	request ImportRequest,
) (ImportStatus, error) {
	var status ImportStatus
	err := m.peerJSON(ctx, http.MethodPost, targetURL+PeerBackupPath+"/imports", token, request, &status)
	return status, err
}

func (m *Manager) peerImportStatus(
	ctx context.Context,
	targetURL, token, transferID string,
) (ImportStatus, error) {
	var status ImportStatus
	err := m.peerJSON(
		ctx,
		http.MethodGet,
		targetURL+PeerBackupPath+"/imports/"+transferID,
		token,
		nil,
		&status,
	)
	return status, err
}

func (m *Manager) peerPutRange(
	ctx context.Context,
	targetURL, token, transferID string,
	index int,
	offset, size, totalSize int64,
	body io.Reader,
	onProgress func(int64),
) error {
	requestCtx, cancel := context.WithTimeout(ctx, 30*time.Minute)
	defer cancel()
	trackedBody := io.Reader(body)
	if onProgress != nil {
		trackedBody = &progressReader{reader: body, onProgress: onProgress}
	}
	request, err := http.NewRequestWithContext(
		requestCtx,
		http.MethodPut,
		targetURL+PeerBackupPath+"/imports/"+transferID+"/ranges/"+strconv.Itoa(index),
		trackedBody,
	)
	if err != nil {
		return err
	}
	request.Header.Set("Authorization", "Bearer "+token)
	request.Header.Set("Content-Type", "application/octet-stream")
	request.Header.Set("Accept", "application/json")
	request.Header.Set(
		"Content-Range",
		fmt.Sprintf("bytes %d-%d/%d", offset, offset+size-1, totalSize),
	)
	request.ContentLength = size
	response, err := m.client.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode > 299 {
		return decodePeerError(response)
	}
	_, err = io.Copy(io.Discard, io.LimitReader(response.Body, maxPeerResponseBytes))
	return err
}

type progressReader struct {
	reader     io.Reader
	onProgress func(int64)
}

func (r *progressReader) Read(buffer []byte) (int, error) {
	read, err := r.reader.Read(buffer)
	if read > 0 {
		r.onProgress(int64(read))
	}
	return read, err
}

func (m *Manager) peerFinalizeImport(
	ctx context.Context,
	targetURL, token, transferID string,
) (ImportStatus, error) {
	var status ImportStatus
	err := m.peerJSON(
		ctx,
		http.MethodPost,
		targetURL+PeerBackupPath+"/imports/"+transferID+"/finalize",
		token,
		struct{}{},
		&status,
	)
	return status, err
}

func (m *Manager) peerCancelImport(ctx context.Context, targetURL, token, transferID string) error {
	return m.peerJSON(
		ctx,
		http.MethodDelete,
		targetURL+PeerBackupPath+"/imports/"+transferID,
		token,
		nil,
		nil,
	)
}

func (m *Manager) peerJSON(
	ctx context.Context,
	method, endpoint, token string,
	body any,
	destination any,
) error {
	requestCtx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	var reader io.Reader
	if body != nil {
		encoded, err := json.Marshal(body)
		if err != nil {
			return err
		}
		reader = bytes.NewReader(encoded)
	}
	request, err := http.NewRequestWithContext(requestCtx, method, endpoint, reader)
	if err != nil {
		return err
	}
	request.Header.Set("Authorization", "Bearer "+token)
	request.Header.Set("Accept", "application/json")
	if body != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	response, err := m.client.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode > 299 {
		return decodePeerError(response)
	}
	if destination == nil || response.StatusCode == http.StatusNoContent {
		_, err := io.Copy(io.Discard, io.LimitReader(response.Body, maxPeerResponseBytes))
		return err
	}
	responseBody, err := io.ReadAll(io.LimitReader(response.Body, maxPeerResponseBytes+1))
	if err != nil {
		return err
	}
	if int64(len(responseBody)) > maxPeerResponseBytes {
		return permanentTransferFailure(errors.New("目标服务器响应过大"))
	}
	decoder := json.NewDecoder(bytes.NewReader(responseBody))
	if err := decoder.Decode(destination); err != nil {
		return permanentTransferFailure(fmt.Errorf("目标服务器响应无效: %w", err))
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return permanentTransferFailure(errors.New("目标服务器响应包含多余数据"))
	}
	return nil
}

func decodePeerError(response *http.Response) error {
	message := ""
	data, _ := io.ReadAll(io.LimitReader(response.Body, maxPeerResponseBytes+1))
	if int64(len(data)) <= maxPeerResponseBytes {
		var payload struct {
			Error string `json:"error"`
		}
		if json.Unmarshal(data, &payload) == nil {
			message = strings.TrimSpace(payload.Error)
		}
		if message == "" {
			message = strings.TrimSpace(string(data))
		}
	}
	if message == "" {
		message = "目标服务器返回 HTTP " + strconv.Itoa(response.StatusCode)
	}
	if response.StatusCode == http.StatusGone {
		return ErrImportCanceled
	}
	return &peerHTTPError{Status: response.StatusCode, Message: message}
}

func transferErrorTemporary(err error) bool {
	if err == nil {
		return false
	}
	var permanent *nonRetryableTransferError
	if errors.As(err, &permanent) {
		return false
	}
	var peerErr *peerHTTPError
	if errors.As(err, &peerErr) {
		return peerErr.temporary()
	}
	return true
}
