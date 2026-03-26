package auth

import (
	"net/http"
	"regexp"
	"strings"
)

const (
	DefaultRegion    = "us-east-1"
	DefaultAccountID = "000000000000"
)

type Context struct {
	AccessKey string
	Region    string
	AccountID string
	Service   string
}

var authHeaderRe = regexp.MustCompile(
	`AWS4-HMAC-SHA256 Credential=([^/]+)/[^/]+/([^/]+)/([^/,]+)`,
)

func Extract(r *http.Request) Context {
	ctx := Context{
		Region:    DefaultRegion,
		AccountID: DefaultAccountID,
	}

	auth := r.Header.Get("Authorization")
	if m := authHeaderRe.FindStringSubmatch(auth); len(m) == 4 {
		ctx.AccessKey = m[1]
		ctx.Region = m[2]
		ctx.Service = m[3]
	}

	if cred := r.URL.Query().Get("X-Amz-Credential"); cred != "" {
		parts := strings.Split(cred, "/")
		if len(parts) >= 4 {
			ctx.AccessKey = parts[0]
			ctx.Region = parts[2]
			ctx.Service = parts[3]
		}
	}

	if region := r.Header.Get("X-Amz-Region"); region != "" {
		ctx.Region = region
	}

	return ctx
}
