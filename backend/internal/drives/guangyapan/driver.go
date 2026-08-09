package guangyapan

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/aliyun/aliyun-oss-go-sdk/oss"
	"github.com/go-resty/resty/v2"

	"github.com/video-site/backend/internal/drives"
)

const (
	Kind = "guangyapan"

	defaultAccountBaseURL = "https://account.guangyapan.com"
	defaultAPIBaseURL     = "https://api.guangyapan.com"
	defaultClientID       = "aMe-8VSlkrbQXpUR"
	defaultPageSize       = 100
)

type Driver struct {
	id             string
	rootID         string
	rootPath       string
	phoneNumber    string
	captchaToken   string
	sendCode       bool
	verifyCode     string
	verificationID string
	accessToken    string
	refreshToken   string
	clientID       string
	deviceID       string
	pageSize       int
	orderBy        int
	sortType       int
	accountBaseURL string
	apiBaseURL     string
	accountClient  *resty.Client
	apiClient      *resty.Client

	onCredentialsUpdate func(map[string]string)

	fileMu sync.RWMutex
	files  map[string]drives.Entry
}

type Config struct {
	ID             string
	RootID         string
	RootPath       string
	PhoneNumber    string
	CaptchaToken   string
	SendCode       bool
	VerifyCode     string
	VerificationID string
	AccessToken    string
	RefreshToken   string
	ClientID       string
	DeviceID       string
	PageSize       int
	OrderBy        int
	SortType       int
	AccountBaseURL string
	APIBaseURL     string

	OnCredentialsUpdate func(map[string]string)
}

func New(c Config) *Driver {
	rootID := strings.TrimSpace(c.RootID)
	if rootID == "0" {
		rootID = ""
	}
	clientID := strings.TrimSpace(c.ClientID)
	if clientID == "" {
		clientID = defaultClientID
	}
	deviceID := normalizeDeviceID(c.DeviceID)
	if deviceID == "" {
		deviceID = randomDeviceID()
	}
	pageSize := c.PageSize
	if pageSize <= 0 {
		pageSize = defaultPageSize
	}
	orderBy := c.OrderBy
	if orderBy < 0 {
		orderBy = 3
	}
	sortType := c.SortType
	if sortType != 0 && sortType != 1 {
		sortType = 1
	}
	accountBaseURL := strings.TrimRight(strings.TrimSpace(c.AccountBaseURL), "/")
	if accountBaseURL == "" {
		accountBaseURL = defaultAccountBaseURL
	}
	apiBaseURL := strings.TrimRight(strings.TrimSpace(c.APIBaseURL), "/")
	if apiBaseURL == "" {
		apiBaseURL = defaultAPIBaseURL
	}
	d := &Driver{
		id:                  strings.TrimSpace(c.ID),
		rootID:              rootID,
		rootPath:            strings.TrimSpace(c.RootPath),
		phoneNumber:         strings.TrimSpace(c.PhoneNumber),
		captchaToken:        strings.TrimSpace(c.CaptchaToken),
		sendCode:            c.SendCode,
		verifyCode:          strings.TrimSpace(c.VerifyCode),
		verificationID:      strings.TrimSpace(c.VerificationID),
		accessToken:         normalizeAccessToken(c.AccessToken),
		refreshToken:        strings.TrimSpace(c.RefreshToken),
		clientID:            clientID,
		deviceID:            deviceID,
		pageSize:            pageSize,
		orderBy:             orderBy,
		sortType:            sortType,
		accountBaseURL:      accountBaseURL,
		apiBaseURL:          apiBaseURL,
		onCredentialsUpdate: c.OnCredentialsUpdate,
		files:               make(map[string]drives.Entry),
	}
	d.accountClient = d.newAccountClient()
	d.apiClient = d.newAPIClient()
	return d
}

func (d *Driver) Kind() string   { return Kind }
func (d *Driver) ID() string     { return d.id }
func (d *Driver) RootID() string { return d.rootID }

func (d *Driver) Init(ctx context.Context) error {
	d.saveCredentials()

	if d.accessToken != "" {
		if err := d.validateToken(ctx); err == nil {
			return d.prepareRootFolder(ctx)
		}
		d.accessToken = ""
	}
	if d.refreshToken != "" {
		if err := d.refresh(ctx); err == nil {
			if err := d.validateToken(ctx); err == nil {
				return d.prepareRootFolder(ctx)
			}
		}
	}
	if d.phoneNumber != "" && d.verifyCode != "" {
		if err := d.loginBySMSCode(ctx); err != nil {
			return err
		}
		if err := d.validateToken(ctx); err != nil {
			return err
		}
		return d.prepareRootFolder(ctx)
	}
	if d.phoneNumber != "" && d.sendCode {
		if err := d.prepareSMSCode(ctx); err != nil {
			return err
		}
		return errors.New("光鸭验证码已发送，请填写 verify_code 后再次保存")
	}
	return errors.New("guangyapan init: provide access_token / refresh_token, or use QR login in admin")
}

func (d *Driver) List(ctx context.Context, dirID string) ([]drives.Entry, error) {
	return d.list(ctx, dirID, true)
}

func (d *Driver) list(ctx context.Context, dirID string, applyDefaultRoot bool) ([]drives.Entry, error) {
	if applyDefaultRoot && strings.TrimSpace(dirID) == "" {
		dirID = d.rootID
	}
	if err := d.ensureAccessToken(ctx); err != nil {
		return nil, err
	}
	out := make([]drives.Entry, 0, d.pageSize)
	for pageNo := 0; ; pageNo++ {
		var resp listResp
		if err := d.postAPI(ctx, "/userres/v1/file/get_file_list", map[string]any{
			"parentId":  dirID,
			"page":      pageNo,
			"pageSize":  d.pageSize,
			"orderBy":   d.orderBy,
			"sortType":  d.sortType,
			"fileTypes": []int{},
		}, &resp); err != nil {
			return nil, err
		}
		for _, item := range resp.Data.List {
			entry := fileItemToEntry(item, dirID)
			out = append(out, entry)
			d.remember(entry)
		}
		if len(resp.Data.List) < d.pageSize {
			return out, nil
		}
		if resp.Data.Total > 0 && len(out) >= resp.Data.Total {
			return out, nil
		}
	}
}

func (d *Driver) Stat(ctx context.Context, fileID string) (*drives.Entry, error) {
	d.fileMu.RLock()
	entry, ok := d.files[fileID]
	d.fileMu.RUnlock()
	if !ok {
		return nil, drives.ErrNotSupported
	}
	return &entry, nil
}

func (d *Driver) StreamURL(ctx context.Context, fileID string) (*drives.StreamLink, error) {
	if strings.TrimSpace(fileID) == "" {
		return nil, errors.New("guangyapan stream: empty file id")
	}
	if err := d.ensureAccessToken(ctx); err != nil {
		return nil, err
	}
	var resp downloadResp
	if err := d.postAPI(ctx, "/nd.bizuserres.s/v1/get_res_download_url", map[string]any{
		"fileId": fileID,
	}, &resp); err != nil {
		return nil, err
	}
	u := strings.TrimSpace(resp.Data.SignedURL)
	if u == "" {
		u = strings.TrimSpace(resp.Data.DownloadURL)
	}
	if u == "" {
		return nil, errors.New("guangyapan stream: empty download url")
	}
	return &drives.StreamLink{URL: u, Headers: http.Header{}, Expires: time.Now().Add(10 * time.Minute), ClientRedirectSafe: true}, nil
}

func (d *Driver) Upload(ctx context.Context, parentID, name string, r io.Reader, size int64) (string, error) {
	if err := d.ensureAccessToken(ctx); err != nil {
		return "", err
	}
	parentID = strings.TrimSpace(parentID)
	if parentID == "" {
		parentID = d.rootID
	}
	name = strings.TrimSpace(name)
	if name == "" {
		return "", errors.New("guangyapan upload: empty file name")
	}
	if r == nil {
		return "", errors.New("guangyapan upload: nil reader")
	}
	if size < 0 {
		return "", errors.New("guangyapan upload: invalid file size")
	}
	token, code, err := d.getUploadToken(ctx, parentID, name, size)
	if err != nil {
		return "", err
	}
	taskID := strings.TrimSpace(token.TaskID)
	if code == 156 {
		return d.waitUploadTaskInfo(ctx, taskID)
	}
	if token.ObjectPath == "" || token.BucketName == "" || token.EndPoint == "" || token.AccessKeyID == "" || token.SecretAccessKey == "" {
		return "", errors.New("guangyapan upload: incomplete upload token")
	}

	client, err := oss.New(normalizeOSSEndpoint(token.EndPoint, token.BucketName), token.AccessKeyID, token.SecretAccessKey, oss.SecurityToken(token.SessionToken))
	if err != nil {
		return "", fmt.Errorf("guangyapan upload: create oss client: %w", err)
	}
	bucket, err := client.Bucket(token.BucketName)
	if err != nil {
		return "", fmt.Errorf("guangyapan upload: create oss bucket: %w", err)
	}
	if size == 0 {
		if err := bucket.PutObject(token.ObjectPath, strings.NewReader("")); err != nil {
			return "", err
		}
	} else if err := multipartUploadToOSS(ctx, bucket, token.ObjectPath, r, size); err != nil {
		return "", err
	}
	fileID, err := d.waitUploadTaskInfo(ctx, taskID)
	if err != nil {
		return "", err
	}
	d.remember(drives.Entry{ID: fileID, ParentID: parentID, Name: name, Size: size})
	return fileID, nil
}

func (d *Driver) EnsureDir(ctx context.Context, pathFromRoot string) (string, error) {
	if err := d.ensureAccessToken(ctx); err != nil {
		return "", err
	}
	clean := strings.Trim(strings.ReplaceAll(strings.TrimSpace(pathFromRoot), "\\", "/"), "/")
	if clean == "" {
		return d.rootID, nil
	}
	parentID := d.rootID
	for _, name := range strings.Split(clean, "/") {
		name = strings.TrimSpace(name)
		if name == "" {
			continue
		}
		childID, err := d.findChildFolderID(ctx, parentID, name)
		if err == nil {
			parentID = childID
			continue
		}
		created, err := d.createDir(ctx, parentID, name)
		if err != nil {
			return "", err
		}
		parentID = created
	}
	return parentID, nil
}

func (d *Driver) Remove(ctx context.Context, fileID string) error {
	if err := d.ensureAccessToken(ctx); err != nil {
		return err
	}
	fileID = strings.TrimSpace(fileID)
	if fileID == "" {
		return errors.New("guangyapan remove: empty file id")
	}
	var resp deleteResp
	if err := d.postAPI(ctx, "/nd.bizuserres.s/v1/file/delete_file", map[string]any{
		"fileIds": []string{fileID},
	}, &resp); err != nil {
		return err
	}
	if !successMessage(resp.Msg) {
		return fmt.Errorf("guangyapan remove: %s", strings.TrimSpace(resp.Msg))
	}
	if taskID := strings.TrimSpace(resp.Data.TaskID); taskID != "" {
		return d.waitTaskDone(ctx, taskID)
	}
	return nil
}

func (d *Driver) Rename(ctx context.Context, fileID, newName string) error {
	if err := d.ensureAccessToken(ctx); err != nil {
		return err
	}
	fileID = strings.TrimSpace(fileID)
	if fileID == "" {
		return errors.New("guangyapan rename: empty file id")
	}
	newName = strings.TrimSpace(newName)
	if newName == "" {
		return errors.New("guangyapan rename: empty new name")
	}
	var resp struct {
		Code int    `json:"code"`
		Msg  string `json:"msg"`
	}
	if err := d.postAPI(ctx, "/nd.bizuserres.s/v1/file/rename", map[string]any{
		"fileId":  fileID,
		"newName": newName,
	}, &resp); err != nil {
		return err
	}
	if !successMessage(resp.Msg) {
		return fmt.Errorf("guangyapan rename: %s", strings.TrimSpace(resp.Msg))
	}
	return nil
}

func (d *Driver) prepareRootFolder(ctx context.Context) error {
	if d.rootPath == "" {
		return nil
	}
	rootID, err := d.resolveFolderPath(ctx, d.rootPath)
	if err != nil {
		return err
	}
	d.rootID = rootID
	return nil
}

func (d *Driver) resolveFolderPath(ctx context.Context, rootPath string) (string, error) {
	clean := strings.Trim(strings.ReplaceAll(strings.TrimSpace(rootPath), "\\", "/"), "/")
	if clean == "" {
		return "", nil
	}
	parentID := ""
	for _, name := range strings.Split(clean, "/") {
		if name == "" {
			continue
		}
		childID, err := d.findChildFolderID(ctx, parentID, name)
		if err != nil {
			return "", err
		}
		parentID = childID
	}
	return parentID, nil
}

func (d *Driver) findChildFolderID(ctx context.Context, parentID, name string) (string, error) {
	entries, err := d.list(ctx, parentID, false)
	if err != nil {
		return "", err
	}
	for _, entry := range entries {
		if entry.IsDir && entry.Name == name {
			return entry.ID, nil
		}
	}
	if parentID == "" {
		return "", fmt.Errorf("guangyapan folder %q not found under /", name)
	}
	return "", fmt.Errorf("guangyapan folder %q not found under parent %s", name, parentID)
}

func (d *Driver) createDir(ctx context.Context, parentID, name string) (string, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return "", errors.New("guangyapan create dir: empty name")
	}
	var resp createDirResp
	if err := d.postAPI(ctx, "/nd.bizuserres.s/v1/file/create_dir", map[string]any{
		"parentId": parentID,
		"dirName":  name,
	}, &resp); err != nil {
		return "", err
	}
	if !successMessage(resp.Msg) {
		return "", fmt.Errorf("guangyapan create dir: %s", strings.TrimSpace(resp.Msg))
	}
	id := strings.TrimSpace(resp.Data.FileID)
	if id == "" {
		return "", errors.New("guangyapan create dir: empty file id")
	}
	d.remember(drives.Entry{ID: id, ParentID: parentID, Name: name, IsDir: true})
	return id, nil
}

func (d *Driver) ensureAccessToken(ctx context.Context) error {
	if strings.TrimSpace(d.accessToken) != "" {
		return nil
	}
	if strings.TrimSpace(d.refreshToken) != "" {
		return d.refresh(ctx)
	}
	if d.phoneNumber != "" && d.verifyCode != "" {
		return d.loginBySMSCode(ctx)
	}
	return errors.New("guangyapan auth: access token is empty; use QR login in admin or provide refresh_token")
}

func (d *Driver) validateToken(ctx context.Context) error {
	var out userMeResp
	resp, err := d.accountClient.R().
		SetContext(ctx).
		SetHeader("Authorization", "Bearer "+d.accessToken).
		SetResult(&out).
		Get("/v1/user/me")
	if err != nil {
		return err
	}
	if resp.IsError() {
		return fmt.Errorf("guangyapan validate token: status=%d body=%s", resp.StatusCode(), resp.String())
	}
	if strings.TrimSpace(out.Sub) == "" {
		return errors.New("guangyapan validate token: empty user sub")
	}
	return nil
}

func (d *Driver) refresh(ctx context.Context) error {
	if strings.TrimSpace(d.refreshToken) == "" {
		return errors.New("guangyapan refresh: refresh_token is empty")
	}
	var out tokenResp
	resp, err := d.accountClient.R().
		SetContext(ctx).
		SetBody(map[string]any{
			"client_id":     d.clientID,
			"grant_type":    "refresh_token",
			"refresh_token": d.refreshToken,
		}).
		SetResult(&out).
		Post("/v1/auth/token")
	if err != nil {
		return err
	}
	if resp.IsError() || out.Error != "" || strings.TrimSpace(out.AccessToken) == "" {
		return fmt.Errorf("guangyapan refresh: %s", accountErr(out.ErrorDesc, out.Error, resp))
	}
	d.accessToken = strings.TrimSpace(out.AccessToken)
	if strings.TrimSpace(out.RefreshToken) != "" {
		d.refreshToken = strings.TrimSpace(out.RefreshToken)
	}
	d.saveCredentials()
	return nil
}

func (d *Driver) loginBySMSCode(ctx context.Context) error {
	verificationID := strings.TrimSpace(d.verificationID)
	if verificationID == "" {
		var err error
		verificationID, err = d.requestVerificationID(ctx)
		if err != nil {
			return err
		}
	}

	var step2 verifyResp
	resp, err := d.accountClient.R().
		SetContext(ctx).
		SetBody(map[string]any{
			"verification_id":   verificationID,
			"verification_code": d.verifyCode,
			"client_id":         d.clientID,
		}).
		SetResult(&step2).
		Post("/v1/auth/verification/verify")
	if err != nil {
		return err
	}
	if resp.IsError() || step2.Error != "" || strings.TrimSpace(step2.VerificationToken) == "" {
		return fmt.Errorf("guangyapan verify code: %s", accountErr(step2.ErrorDesc, step2.Error, resp))
	}

	var out tokenResp
	resp, err = d.accountClient.R().
		SetContext(ctx).
		SetBody(map[string]any{
			"verification_code":  d.verifyCode,
			"verification_token": step2.VerificationToken,
			"username":           normalizePhoneE164(d.phoneNumber),
			"client_id":          d.clientID,
		}).
		SetResult(&out).
		Post("/v1/auth/signin")
	if err != nil {
		return err
	}
	if resp.IsError() || out.Error != "" || strings.TrimSpace(out.AccessToken) == "" {
		return fmt.Errorf("guangyapan signin: %s", accountErr(out.ErrorDesc, out.Error, resp))
	}
	d.accessToken = strings.TrimSpace(out.AccessToken)
	d.refreshToken = strings.TrimSpace(out.RefreshToken)
	d.verificationID = ""
	d.verifyCode = ""
	d.sendCode = false
	d.saveCredentials()
	return nil
}

func (d *Driver) prepareSMSCode(ctx context.Context) error {
	d.verificationID = ""
	if err := d.ensureCaptchaToken(ctx, false); err != nil {
		return err
	}
	id, err := d.requestVerificationID(ctx)
	if err != nil {
		return err
	}
	d.verificationID = id
	d.sendCode = false
	d.saveCredentials()
	return nil
}

func (d *Driver) requestVerificationID(ctx context.Context) (string, error) {
	if d.captchaToken != "" {
		d.accountClient.SetHeader("X-Captcha-Token", d.captchaToken)
	}
	var out verificationResp
	resp, err := d.accountClient.R().
		SetContext(ctx).
		SetBody(map[string]any{
			"phone_number": normalizePhoneE164(d.phoneNumber),
			"target":       "ANY",
			"client_id":    d.clientID,
		}).
		SetResult(&out).
		Post("/v1/auth/verification")
	if err != nil {
		return "", err
	}
	if resp.IsError() || out.Error != "" || strings.TrimSpace(out.VerificationID) == "" {
		if strings.Contains(out.Error, "captcha_invalid") || strings.Contains(out.ErrorDesc, "captcha_token expired") {
			if err := d.ensureCaptchaToken(ctx, true); err == nil {
				return d.requestVerificationID(ctx)
			}
		}
		return "", fmt.Errorf("guangyapan request verification: %s", accountErr(out.ErrorDesc, out.Error, resp))
	}
	return strings.TrimSpace(out.VerificationID), nil
}

func (d *Driver) ensureCaptchaToken(ctx context.Context, force bool) error {
	if !force && d.captchaToken != "" {
		d.accountClient.SetHeader("X-Captcha-Token", d.captchaToken)
		return nil
	}
	var out captchaInitResp
	resp, err := d.accountClient.R().
		SetContext(ctx).
		SetBody(map[string]any{
			"client_id": d.clientID,
			"action":    "POST:/v1/auth/verification",
			"device_id": d.deviceID,
			"meta": map[string]any{
				"username":           normalizePhoneE164(d.phoneNumber),
				"phone_number":       normalizePhoneE164(d.phoneNumber),
				"VERIFICATION_PHONE": normalizePhoneE164(d.phoneNumber),
			},
		}).
		SetResult(&out).
		Post("/v1/shield/captcha/init")
	if err != nil {
		return err
	}
	if resp.IsError() || out.Error != "" || strings.TrimSpace(out.CaptchaToken) == "" {
		return fmt.Errorf("guangyapan captcha init: %s", accountErr(out.ErrorDesc, out.Error, resp))
	}
	d.captchaToken = strings.TrimSpace(out.CaptchaToken)
	d.accountClient.SetHeader("X-Captcha-Token", d.captchaToken)
	d.saveCredentials()
	return nil
}

func (d *Driver) postAPI(ctx context.Context, p string, body any, out any) error {
	if strings.TrimSpace(d.accessToken) == "" {
		return errors.New("guangyapan api: access token is empty")
	}
	resp, err := d.apiClient.R().
		SetContext(ctx).
		SetHeader("Authorization", "Bearer "+d.accessToken).
		SetBody(body).
		SetResult(out).
		Post(p)
	if err != nil {
		return err
	}
	if resp.StatusCode() == http.StatusUnauthorized || resp.StatusCode() == http.StatusForbidden {
		if strings.TrimSpace(d.refreshToken) == "" {
			code, msg := guangYaPanResponseCodeMsg(resp, out)
			if guangYaPanLooksRateLimited(resp.StatusCode(), code, msg) {
				return guangYaPanRateLimitError(p, resp.Header().Get("Retry-After"), resp.StatusCode(), code, msg)
			}
			return fmt.Errorf("guangyapan api: status=%d body=%s", resp.StatusCode(), resp.String())
		}
		if err := d.refresh(ctx); err != nil {
			return err
		}
		resp, err = d.apiClient.R().
			SetContext(ctx).
			SetHeader("Authorization", "Bearer "+d.accessToken).
			SetBody(body).
			SetResult(out).
			Post(p)
		if err != nil {
			return err
		}
	}
	if resp.IsError() {
		code, msg := guangYaPanResponseCodeMsg(resp, out)
		if guangYaPanLooksRateLimited(resp.StatusCode(), code, msg) {
			return guangYaPanRateLimitError(p, resp.Header().Get("Retry-After"), resp.StatusCode(), code, msg)
		}
		return fmt.Errorf("guangyapan api: status=%d body=%s", resp.StatusCode(), resp.String())
	}
	code, msg := guangYaPanResponseCodeMsg(resp, out)
	if guangYaPanLooksRateLimited(resp.StatusCode(), code, msg) {
		return guangYaPanRateLimitError(p, resp.Header().Get("Retry-After"), resp.StatusCode(), code, msg)
	}
	return nil
}

func guangYaPanResponseCodeMsg(resp *resty.Response, out any) (int, string) {
	if resp != nil {
		body := resp.Body()
		if len(body) > 0 {
			var env struct {
				Code int    `json:"code"`
				Msg  string `json:"msg"`
			}
			if err := json.Unmarshal(body, &env); err == nil && (env.Code != 0 || strings.TrimSpace(env.Msg) != "") {
				return env.Code, strings.TrimSpace(env.Msg)
			}
			if resp.IsError() {
				return 0, strings.TrimSpace(resp.String())
			}
		}
	}
	if code, msg, ok := guangYaPanCodeMsgFromValue(out); ok {
		return code, msg
	}
	if resp != nil && resp.IsError() {
		return 0, strings.TrimSpace(resp.String())
	}
	return 0, ""
}

func guangYaPanCodeMsgFromValue(v any) (int, string, bool) {
	rv := reflect.ValueOf(v)
	for rv.IsValid() && rv.Kind() == reflect.Pointer {
		if rv.IsNil() {
			return 0, "", false
		}
		rv = rv.Elem()
	}
	if !rv.IsValid() || rv.Kind() != reflect.Struct {
		return 0, "", false
	}
	codeField := rv.FieldByName("Code")
	msgField := rv.FieldByName("Msg")
	if !codeField.IsValid() && !msgField.IsValid() {
		return 0, "", false
	}
	code := 0
	if codeField.IsValid() && codeField.CanInt() {
		code = int(codeField.Int())
	}
	msg := ""
	if msgField.IsValid() && msgField.Kind() == reflect.String {
		msg = strings.TrimSpace(msgField.String())
	}
	return code, msg, true
}

func guangYaPanLooksRateLimited(status int, code int, _ string) bool {
	if status == http.StatusTooManyRequests || code == http.StatusTooManyRequests {
		return true
	}
	switch status {
	case http.StatusInternalServerError, http.StatusBadGateway, http.StatusServiceUnavailable, http.StatusGatewayTimeout, 509:
		return true
	}
	return false
}

func guangYaPanRateLimitError(step, retryAfter string, status int, code int, message string) error {
	message = strings.TrimSpace(message)
	if message == "" {
		message = "guangyapan api rate limited"
	}
	if len(message) > 1024 {
		message = message[:1024] + "...(truncated)"
	}
	return &drives.RateLimitError{
		Provider:   Kind,
		RetryAfter: parseRetryAfterHeader(retryAfter),
		Err:        fmt.Errorf("guangyapan api rate limited: step=%s status=%d code=%d msg=%s", step, status, code, message),
	}
}

func parseRetryAfterHeader(raw string) time.Duration {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return 0
	}
	if seconds, err := strconv.Atoi(raw); err == nil && seconds > 0 {
		return time.Duration(seconds) * time.Second
	}
	if when, err := http.ParseTime(raw); err == nil {
		d := time.Until(when)
		if d > 0 {
			return d
		}
	}
	return 0
}

func (d *Driver) waitTaskDone(ctx context.Context, taskID string) error {
	const (
		maxTry   = 30
		interval = 300 * time.Millisecond
	)
	for i := 0; i < maxTry; i++ {
		var out taskStatusResp
		if err := d.postAPI(ctx, "/nd.bizuserres.s/v1/get_task_status", map[string]any{"taskId": taskID}, &out); err != nil {
			return err
		}
		if !successMessage(out.Msg) {
			return fmt.Errorf("guangyapan task status: %s", strings.TrimSpace(out.Msg))
		}
		switch out.Data.Status {
		case 2:
			return nil
		case -1, 3:
			return fmt.Errorf("guangyapan task %s failed with status=%d", taskID, out.Data.Status)
		}
		if i == maxTry-1 {
			break
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(interval):
		}
	}
	return fmt.Errorf("guangyapan task %s timeout", taskID)
}

func (d *Driver) getUploadToken(ctx context.Context, parentID, name string, size int64) (*uploadTokenData, int, error) {
	var out uploadTokenResp
	if err := d.postAPI(ctx, "/nd.bizuserres.s/v1/get_res_center_token", map[string]any{
		"capacity": 2,
		"name":     name,
		"parentId": parentID,
		"res":      map[string]any{"fileSize": size},
	}, &out); err != nil {
		return nil, 0, err
	}
	if strings.TrimSpace(out.Msg) != "" && !successMessage(out.Msg) {
		return nil, out.Code, fmt.Errorf("guangyapan upload token: %s", strings.TrimSpace(out.Msg))
	}
	if out.Data.TaskID == "" {
		return nil, out.Code, errors.New("guangyapan upload token: empty task id")
	}
	if out.Data.AccessKeyID == "" {
		out.Data.AccessKeyID = out.Data.Creds.AccessKeyID
	}
	if out.Data.SecretAccessKey == "" {
		out.Data.SecretAccessKey = out.Data.Creds.SecretAccessKey
	}
	if out.Data.SessionToken == "" {
		out.Data.SessionToken = out.Data.Creds.SessionToken
	}
	if strings.TrimSpace(out.Data.EndPoint) == "" {
		out.Data.EndPoint = strings.TrimSpace(out.Data.FullEndPoint)
	}
	if strings.TrimSpace(out.Data.EndPoint) != "" && !strings.HasPrefix(out.Data.EndPoint, "http://") && !strings.HasPrefix(out.Data.EndPoint, "https://") {
		if strings.TrimSpace(out.Data.FullEndPoint) != "" {
			out.Data.EndPoint = strings.TrimSpace(out.Data.FullEndPoint)
		} else if strings.TrimSpace(out.Data.BucketName) != "" {
			host := strings.TrimSpace(out.Data.EndPoint)
			prefix := strings.TrimSpace(out.Data.BucketName) + "."
			if strings.HasPrefix(host, prefix) {
				out.Data.EndPoint = "https://" + host
			} else {
				out.Data.EndPoint = "https://" + strings.TrimSpace(out.Data.BucketName) + "." + host
			}
		} else {
			out.Data.EndPoint = "https://" + strings.TrimSpace(out.Data.EndPoint)
		}
	}
	return &out.Data, out.Code, nil
}

func (d *Driver) waitUploadTaskInfo(ctx context.Context, taskID string) (string, error) {
	const (
		maxTry   = 300
		interval = time.Second
	)
	for i := 0; i < maxTry; i++ {
		var out taskInfoResp
		if err := d.postAPI(ctx, "/nd.bizuserres.s/v1/file/get_info_by_task_id", map[string]any{"taskId": taskID}, &out); err != nil {
			return "", err
		}
		if out.Data.FileID != "" {
			return out.Data.FileID, nil
		}
		switch out.Code {
		case 0, 145, 146, 147, 155, 163:
		default:
			if strings.TrimSpace(out.Msg) != "" {
				return "", fmt.Errorf("guangyapan upload task failed: code=%d msg=%s", out.Code, strings.TrimSpace(out.Msg))
			}
		}
		if i == maxTry-1 {
			break
		}
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case <-time.After(interval):
		}
	}
	return "", fmt.Errorf("guangyapan upload task %s timeout", taskID)
}

func multipartUploadToOSS(ctx context.Context, bucket *oss.Bucket, objectPath string, r io.Reader, size int64) error {
	partSize := calcUploadPartSize(size)
	upload, err := bucket.InitiateMultipartUpload(objectPath, oss.Sequential())
	if err != nil {
		return err
	}
	partCount := int((size + partSize - 1) / partSize)
	parts := make([]oss.UploadPart, 0, partCount)
	uploaded := int64(0)
	partNumber := 1
	for uploaded < size {
		if err := ctx.Err(); err != nil {
			return err
		}
		cur := partSize
		if left := size - uploaded; left < cur {
			cur = left
		}
		part, err := bucket.UploadPart(upload, &contextReader{ctx: ctx, r: io.LimitReader(r, cur)}, cur, partNumber)
		if err != nil {
			return err
		}
		parts = append(parts, part)
		uploaded += cur
		partNumber++
	}
	_, err = bucket.CompleteMultipartUpload(upload, parts)
	return err
}

type contextReader struct {
	ctx context.Context
	r   io.Reader
}

func (r *contextReader) Read(p []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	return r.r.Read(p)
}

func calcUploadPartSize(size int64) int64 {
	const mb = int64(1024 * 1024)
	const gb = int64(1024 * 1024 * 1024)
	switch {
	case size <= 100*mb:
		return mb
	case size <= 16*gb:
		return 2 * mb
	case size <= 160*gb:
		return 4 * mb
	default:
		return 8 * mb
	}
}

func (d *Driver) newAccountClient() *resty.Client {
	client := resty.New().
		SetTimeout(30*time.Second).
		SetBaseURL(d.accountBaseURL).
		SetHeader("Accept", "application/json, text/plain, */*").
		SetHeader("Content-Type", "application/json").
		SetHeader("X-Device-Model", "chrome%2F147.0.0.0").
		SetHeader("X-Device-Name", "PC-Chrome").
		SetHeader("X-Device-Sign", "wdi10."+d.deviceID+"xxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxx").
		SetHeader("X-Net-Work-Type", "NONE").
		SetHeader("X-OS-Version", "MacIntel").
		SetHeader("X-Platform-Version", "1").
		SetHeader("X-Protocol-Version", "301").
		SetHeader("X-Provider-Name", "NONE").
		SetHeader("X-SDK-Version", "9.0.2").
		SetHeader("X-Client-Id", d.clientID).
		SetHeader("X-Client-Version", "0.0.1").
		SetHeader("X-Device-Id", d.deviceID)
	if d.captchaToken != "" {
		client.SetHeader("X-Captcha-Token", d.captchaToken)
	}
	return client
}

func (d *Driver) newAPIClient() *resty.Client {
	return resty.New().
		SetTimeout(30*time.Second).
		SetBaseURL(d.apiBaseURL).
		SetHeader("Accept", "application/json, text/plain, */*").
		SetHeader("Content-Type", "application/json").
		SetHeader("Did", d.deviceID).
		SetHeader("Dt", "4")
}

func (d *Driver) saveCredentials() {
	if d.onCredentialsUpdate == nil {
		return
	}
	d.onCredentialsUpdate(map[string]string{
		"access_token":    d.accessToken,
		"refresh_token":   d.refreshToken,
		"captcha_token":   d.captchaToken,
		"device_id":       d.deviceID,
		"client_id":       d.clientID,
		"verification_id": d.verificationID,
		"verify_code":     d.verifyCode,
		"send_code":       strconv.FormatBool(d.sendCode),
	})
}

func (d *Driver) remember(entry drives.Entry) {
	if entry.ID == "" {
		return
	}
	d.fileMu.Lock()
	d.files[entry.ID] = entry
	d.fileMu.Unlock()
}

func fileItemToEntry(item fileItem, parentID string) drives.Entry {
	if item.ParentID != "" {
		parentID = item.ParentID
	}
	return drives.Entry{
		ID:       item.FileID,
		Name:     item.FileName,
		Size:     item.FileSize,
		Hash:     normalizeGCID(item.GCID),
		IsDir:    item.ResType == 2,
		ParentID: parentID,
		ModTime:  unixOrZero(item.UTime),
	}
}

func normalizeGCID(value string) string {
	value = strings.TrimSpace(value)
	if len(value) != 40 {
		return ""
	}
	for _, ch := range value {
		switch {
		case ch >= '0' && ch <= '9':
		case ch >= 'a' && ch <= 'f':
		case ch >= 'A' && ch <= 'F':
		default:
			return ""
		}
	}
	return strings.ToUpper(value)
}

func successMessage(msg string) bool {
	return strings.EqualFold(strings.TrimSpace(msg), "success")
}

func accountErr(desc, short string, resp *resty.Response) string {
	msg := strings.TrimSpace(desc)
	if msg == "" {
		msg = strings.TrimSpace(short)
	}
	if msg == "" && resp != nil {
		msg = strings.TrimSpace(resp.String())
	}
	if msg == "" && resp != nil {
		msg = fmt.Sprintf("status=%d", resp.StatusCode())
	}
	if msg == "" {
		msg = "unknown error"
	}
	return msg
}

func normalizeAccessToken(v string) string {
	v = strings.TrimSpace(v)
	if strings.HasPrefix(strings.ToLower(v), "bearer ") {
		return strings.TrimSpace(v[7:])
	}
	return v
}

func normalizeCaptchaUsername(phone string) string {
	p := strings.TrimSpace(phone)
	p = strings.ReplaceAll(p, " ", "")
	p = strings.TrimPrefix(p, "+")
	b := make([]rune, 0, len(p))
	for _, ch := range p {
		if ch >= '0' && ch <= '9' {
			b = append(b, ch)
		}
	}
	digits := string(b)
	if strings.HasPrefix(digits, "86") && len(digits) > 11 {
		digits = digits[2:]
	}
	return digits
}

func normalizePhoneE164(phone string) string {
	p := strings.TrimSpace(phone)
	if p == "" {
		return ""
	}
	p = strings.ReplaceAll(p, " ", "")
	if strings.HasPrefix(p, "+") {
		if strings.HasPrefix(p, "+86") && len(p) > 3 {
			return "+86 " + strings.TrimPrefix(p, "+86")
		}
		return p
	}
	digits := normalizeCaptchaUsername(p)
	if len(digits) == 11 {
		return "+86 " + digits
	}
	return p
}

func normalizeDeviceID(v string) string {
	v = strings.ToLower(strings.TrimSpace(v))
	v = strings.ReplaceAll(v, "-", "")
	if len(v) != 32 {
		return ""
	}
	for _, ch := range v {
		if (ch < '0' || ch > '9') && (ch < 'a' || ch > 'f') {
			return ""
		}
	}
	return v
}

func randomDeviceID() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "0123456789abcdef0123456789abcdef"
	}
	return hex.EncodeToString(b)
}

func normalizeOSSEndpoint(endpoint, bucket string) string {
	ep := strings.TrimSpace(endpoint)
	if ep == "" {
		return ep
	}
	if !strings.HasPrefix(ep, "http://") && !strings.HasPrefix(ep, "https://") {
		ep = "https://" + ep
	}
	u, err := url.Parse(ep)
	if err != nil || u.Host == "" {
		return ep
	}
	prefix := strings.TrimSpace(bucket)
	if prefix != "" && strings.HasPrefix(u.Host, prefix+".") {
		u.Host = strings.TrimPrefix(u.Host, prefix+".")
	}
	return u.String()
}

var _ drives.Drive = (*Driver)(nil)
var _ drives.Remover = (*Driver)(nil)
