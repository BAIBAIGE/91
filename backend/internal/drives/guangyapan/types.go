package guangyapan

import "time"

type tokenResp struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	Error        string `json:"error"`
	ErrorCode    int    `json:"error_code"`
	ErrorDesc    string `json:"error_description"`
}

type verificationResp struct {
	VerificationID string `json:"verification_id"`
	Error          string `json:"error"`
	ErrorCode      int    `json:"error_code"`
	ErrorDesc      string `json:"error_description"`
}

type captchaInitResp struct {
	CaptchaToken string `json:"captcha_token"`
	Error        string `json:"error"`
	ErrorCode    int    `json:"error_code"`
	ErrorDesc    string `json:"error_description"`
}

type verifyResp struct {
	VerificationToken string `json:"verification_token"`
	Error             string `json:"error"`
	ErrorCode         int    `json:"error_code"`
	ErrorDesc         string `json:"error_description"`
}

type userMeResp struct {
	Sub string `json:"sub"`
}

type listResp struct {
	Code int    `json:"code"`
	Msg  string `json:"msg"`
	Data struct {
		Total int        `json:"total"`
		List  []fileItem `json:"list"`
	} `json:"data"`
}

type fileItem struct {
	FileID   string `json:"fileId"`
	ParentID string `json:"parentId"`
	FileName string `json:"fileName"`
	FileSize int64  `json:"fileSize"`
	GCID     string `json:"gcid"`
	ResType  int    `json:"resType"`
	CTime    int64  `json:"ctime"`
	UTime    int64  `json:"utime"`
}

type downloadResp struct {
	Code int    `json:"code"`
	Msg  string `json:"msg"`
	Data struct {
		SignedURL   string `json:"signedURL"`
		DownloadURL string `json:"downloadUrl"`
	} `json:"data"`
}

type createDirResp struct {
	Code int    `json:"code"`
	Msg  string `json:"msg"`
	Data struct {
		FileID   string `json:"fileId"`
		FileName string `json:"fileName"`
		ResType  int    `json:"resType"`
		CTime    int64  `json:"ctime"`
		UTime    int64  `json:"utime"`
	} `json:"data"`
}

type deleteResp struct {
	Code int    `json:"code"`
	Msg  string `json:"msg"`
	Data struct {
		TaskID string `json:"taskId"`
	} `json:"data"`
}

type taskStatusResp struct {
	Code int    `json:"code"`
	Msg  string `json:"msg"`
	Data struct {
		Status int `json:"status"`
	} `json:"data"`
}

type uploadTokenResp struct {
	Code int             `json:"code"`
	Msg  string          `json:"msg"`
	Data uploadTokenData `json:"data"`
}

type uploadTokenData struct {
	AlreadyDone     bool   `json:"-"`
	TaskID          string `json:"taskId"`
	ObjectPath      string `json:"objectPath"`
	BucketName      string `json:"bucketName"`
	EndPoint        string `json:"endPoint"`
	FullEndPoint    string `json:"fullEndPoint"`
	AccessKeyID     string `json:"accessKeyID"`
	SecretAccessKey string `json:"secretAccessKey"`
	SessionToken    string `json:"sessionToken"`
	Creds           struct {
		AccessKeyID     string `json:"accessKeyID"`
		SecretAccessKey string `json:"secretAccessKey"`
		SessionToken    string `json:"sessionToken"`
	} `json:"creds"`
}

type taskInfoResp struct {
	Code int    `json:"code"`
	Msg  string `json:"msg"`
	Data struct {
		FileID string `json:"fileId"`
	} `json:"data"`
}

func unixOrZero(v int64) time.Time {
	if v <= 0 {
		return time.Time{}
	}
	return time.Unix(v, 0)
}
