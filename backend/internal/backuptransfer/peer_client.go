package backuptransfer

import (
	"bytes"
	"context"
	"crypto/sha256"
	"crypto/tls"
	"encoding/base64"
	"encoding/hex"
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
		ForceAttemptHTTP2:     true,
		MaxIdleConns:          8,
		MaxIdleConnsPerHost:   2,
		IdleConnTimeout:       90 * time.Second,
		TLSHandshakeTimeout:   30 * time.Second,
		ResponseHeaderTimeout: 60 * time.Second,
		ExpectContinueTimeout: time.Second,
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
	err := m.peerJSON(ctx, http.MethodGet, targetURL+"/peer/v1/backups/capabilities", token, nil, &capabilities)
	return capabilities, err
}

func (m *Manager) peerBeginImport(
	ctx context.Context,
	targetURL, token string,
	request ImportRequest,
) (ImportStatus, error) {
	var status ImportStatus
	err := m.peerJSON(ctx, http.MethodPost, targetURL+"/peer/v1/backups/imports", token, request, &status)
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
		targetURL+"/peer/v1/backups/imports/"+transferID,
		token,
		nil,
		&status,
	)
	return status, err
}

func (m *Manager) peerPutChunk(
	ctx context.Context,
	targetURL, token, transferID string,
	index int,
	body []byte,
	digestHex string,
) error {
	requestCtx, cancel := context.WithTimeout(ctx, 15*time.Minute)
	defer cancel()
	request, err := http.NewRequestWithContext(
		requestCtx,
		http.MethodPut,
		targetURL+"/peer/v1/backups/imports/"+transferID+"/chunks/"+strconv.Itoa(index),
		bytes.NewReader(body),
	)
	if err != nil {
		return err
	}
	request.Header.Set("Authorization", "Bearer "+token)
	request.Header.Set("Content-Type", "application/octet-stream")
	request.Header.Set("Accept", "application/json")
	request.Header.Set("X-Chunk-SHA256", digestHex)
	if digest, err := hex.DecodeString(digestHex); err == nil && len(digest) == sha256.Size {
		request.Header.Set("Content-Digest", "sha-256=:"+base64.StdEncoding.EncodeToString(digest)+":")
	}
	request.Header.Set("Expect", "100-continue")
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

func (m *Manager) peerFinalizeImport(
	ctx context.Context,
	targetURL, token, transferID string,
) (ImportStatus, error) {
	var status ImportStatus
	err := m.peerJSON(
		ctx,
		http.MethodPost,
		targetURL+"/peer/v1/backups/imports/"+transferID+"/finalize",
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
		targetURL+"/peer/v1/backups/imports/"+transferID,
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
