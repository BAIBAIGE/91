package p115

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"syscall"
	"time"

	sdk "github.com/SheltonZhu/115driver/pkg/driver"
	"github.com/go-resty/resty/v2"
)

const p115ReadTimeout = 15 * time.Second

// newSDKClient gives each SDK operation its own mutable Request field. The
// HTTP client (including its connection pool and cookie jar) is shared. Resty
// headers, cookies and middleware are local so binding ctx cannot affect another
// operation. The SDK creates its own requests and has no context parameter.
// Direct Client.R() calls already create independent requests.
// Do not copy the SDK client: its upload auth fields are mutable and belong to
// the upload path, which serializes access through uploadGate.
func (d *Driver) newSDKClient(ctx context.Context) *sdk.Pan115Client {
	shared := d.client.Client
	client := resty.NewWithClient(shared.GetClient())
	client.Header = shared.Header.Clone()
	client.Cookies = append([]*http.Cookie(nil), shared.Cookies...)
	client.OnBeforeRequest(func(_ *resty.Client, request *resty.Request) error {
		request.SetContext(ctx)
		return ctx.Err()
	})
	return &sdk.Pan115Client{Client: client}
}

// Only file lookup and download-link resolution may use this client. Although
// the download endpoint uses POST, it reads a link; retrying uploads or other
// mutations on transport errors could repeat a completed operation.
func (d *Driver) newSDKReadClient(ctx context.Context) *sdk.Pan115Client {
	client := d.newSDKClient(ctx)
	client.Client.SetRetryCount(1).
		SetRetryWaitTime(200 * time.Millisecond).
		SetRetryMaxWaitTime(200 * time.Millisecond).
		AddRetryCondition(func(_ *resty.Response, err error) bool {
			if ctx.Err() != nil || errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
				return false
			}
			var networkErr net.Error
			return (errors.As(err, &networkErr) && networkErr.Timeout()) ||
				errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) ||
				errors.Is(err, syscall.ECONNRESET) || errors.Is(err, syscall.EPIPE)
		})
	return client
}

func (d *Driver) getFile(ctx context.Context, fileID string) (*sdk.File, error) {
	if d.client == nil || d.client.Client == nil {
		return nil, errors.New("115 client not initialized")
	}
	ctx, cancel := context.WithTimeout(ctx, p115ReadTimeout)
	defer cancel()
	return d.newSDKReadClient(ctx).GetFile(fileID)
}
