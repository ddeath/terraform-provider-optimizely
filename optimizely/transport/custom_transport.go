package transport

import (
	"bytes"
	"context"
	"io/ioutil"
	"math"
	"net/http"
	"strconv"
	"time"
)

var (
	maxRetries                       = 5
	defaultBackOffMultiplier float64 = 2
	defaultBackOffBase       float64 = 2
	defaultHTTPRetryTimeout          = 5 * 60 * time.Second
	rateLimitResetHeader             = "X-Ratelimit-Reset"
)

// CustomTransport holds DefaultTransport configuration and is used to for custom http error handling
type CustomTransport struct {
	defaultTransport http.RoundTripper
}

// RoundTrip method used to retry http errors
func (customTransport *CustomTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	var ccancel context.CancelFunc
	ctx := req.Context()
	if _, set := ctx.Deadline(); !set {
		ctx, ccancel = context.WithTimeout(ctx, defaultHTTPRetryTimeout)
		defer ccancel()
	}

	var rawBody []byte
	if req.Body != nil && req.Body != http.NoBody {
		rawBody, _ = ioutil.ReadAll(req.Body)
		req.Body.Close()
	}
	var resp *http.Response
	var respErr error
	retryCount := 0
	for {
		if retryCount == maxRetries {
			ccancel()
		}

		newRequest := customTransport.cloneRequest(req, &rawBody)
		resp, respErr = customTransport.defaultTransport.RoundTrip(newRequest)
		// Close the body so connection can be re-used
		if resp != nil {
			localVarBody, _ := ioutil.ReadAll(resp.Body)
			resp.Body.Close()
			resp.Body = ioutil.NopCloser(bytes.NewBuffer(localVarBody))
		}
		if respErr != nil {
			return resp, respErr
		}

		// Check if request should be retried and get retry time
		retryDuration, retry := customTransport.retryRequest(resp, retryCount)
		if !retry {
			return resp, respErr
		}

		select {
		case <-ctx.Done():
			return resp, respErr
		case <-time.After(*retryDuration):
			retryCount++
			continue
		}
	}
}

func (t *CustomTransport) cloneRequest(r *http.Request, rawBody *[]byte) *http.Request {
	newRequest := *r

	if r.Body == nil || r.Body == http.NoBody {
		return &newRequest
	}
	newRequest.Body = ioutil.NopCloser(bytes.NewBuffer(*rawBody))

	return &newRequest
}

func (t *CustomTransport) retryRequest(response *http.Response, retryCount int) (*time.Duration, bool) {
	var err error
	if v := response.Header.Get(rateLimitResetHeader); v != "" && response.StatusCode == 429 {
		vInt, err := strconv.ParseInt(v, 10, 64)
		if err == nil {
			retryDuration := time.Duration(vInt) * time.Second
			return &retryDuration, true
		}
	}

	// Calculate retry for 5xx errors or if unable to parse value of rateLimitResetHeader
	if response.StatusCode >= 500 || err != nil {
		// Calculate the retry val (base * multiplier^retryCount)
		retryVal := defaultBackOffBase * math.Pow(defaultBackOffMultiplier, float64(retryCount))
		// retry duration shouldn't exceed default timeout period
		retryVal = math.Min(float64(defaultHTTPRetryTimeout/time.Second), retryVal)
		retryDuration := time.Duration(retryVal) * time.Second
		return &retryDuration, true
	}

	return nil, false
}

// NewCustomTransport returns new CustomTransport struct
func NewCustomTransport(roundTripper http.RoundTripper) *CustomTransport {
	// Use default transport if one provided is nil
	if roundTripper == nil {
		roundTripper = http.DefaultTransport
	}

	customTransport := CustomTransport{
		defaultTransport: roundTripper,
	}

	return &customTransport
}
