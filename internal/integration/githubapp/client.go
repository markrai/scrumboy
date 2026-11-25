package githubapp

import (
	"bytes"
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"scrumboy/internal/integration"
)

const apiRoot = "https://api.github.com"
const maxPages = 20
const maxBody = 4 << 20

var ErrUnconfigured = errors.New("github app not configured")
var ErrUnavailable = errors.New("github app unavailable")
var ErrRevoked = errors.New("github app installation unavailable")
var ErrIncomplete = errors.New("github app repository discovery incomplete")

type cacheEntry struct {
	token string
	expires time.Time
}

type Client struct {
	appID int64
	key *rsa.PrivateKey
	http *http.Client
	baseURL string
	now func() time.Time
	mu sync.Mutex
	cache map[int64]cacheEntry
}

var _ integration.Provider = (*Client)(nil)

func New(appID int64, pemBytes []byte, httpClient *http.Client) (*Client, error) {
	return newClient(appID, pemBytes, httpClient, apiRoot)
}

func newClient(appID int64, pemBytes []byte, httpClient *http.Client, baseURL string) (*Client, error) {
	if appID <= 0 || len(pemBytes) == 0 {
		return nil, fmt.Errorf("%w: app id and RSA private key are required", ErrUnconfigured)
	}
	block, _ := pem.Decode(pemBytes)
	if block == nil {
		return nil, errors.New("invalid GitHub App private key PEM")
	}
	var key *rsa.PrivateKey
	if parsed, err := x509.ParsePKCS1PrivateKey(block.Bytes); err == nil {
		key = parsed
	} else if parsedPKCS8, err := x509.ParsePKCS8PrivateKey(block.Bytes); err == nil {
		var ok bool
		key, ok = parsedPKCS8.(*rsa.PrivateKey)
		if !ok {
			return nil, errors.New("GitHub App private key must be RSA")
		}
	} else {
		return nil, errors.New("invalid GitHub App RSA private key")
	}
	if key.N.BitLen() < 2048 {
		return nil, errors.New("GitHub App RSA key is too small")
	}
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 15*time.Second}
	}
	if _, err := url.ParseRequestURI(baseURL); err != nil {
		return nil, errors.New("invalid GitHub API origin")
	}
	return &Client{appID:appID,key:key,http:httpClient,baseURL:strings.TrimSuffix(baseURL,"/"),now:time.Now,cache:map[int64]cacheEntry{}},nil
}

func (c *Client) Invalidate(raw string) {
	id,err:=strconv.ParseInt(raw,10,64)
	if err!=nil || id<=0{return}
	c.mu.Lock()
	delete(c.cache,id)
	c.mu.Unlock()
}

func (c *Client) appJWT() (string,error) {
	now := c.now().UTC().Unix()
	header := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"RS256","typ":"JWT"}`))
	claims, _ := json.Marshal(map[string]any{"iat":now-60,"exp":now+540,"iss":strconv.FormatInt(c.appID,10)})
	payload := base64.RawURLEncoding.EncodeToString(claims)
	message := header+"."+payload
	digest := sha256.Sum256([]byte(message))
	sig, err := rsa.SignPKCS1v15(rand.Reader,c.key,crypto.SHA256,digest[:])
	if err != nil {return "",errors.New("could not sign GitHub App request")}
	return message+"."+base64.RawURLEncoding.EncodeToString(sig),nil
}

func (c *Client) request(ctx context.Context,method,path,token string,body any,target any) error {
	var encoded io.Reader
	if body != nil {
		raw,err:=json.Marshal(body)
		if err!=nil{return errors.New("could not encode provider request")}
		encoded=bytes.NewReader(raw)
	}
	req,err:=http.NewRequestWithContext(ctx,method,c.baseURL+path,encoded)
	if err!=nil{return fmt.Errorf("%w: invalid request",ErrUnavailable)}
	req.Header.Set("Authorization","Bearer "+token)
	req.Header.Set("Accept","application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version","2022-11-28")
	if body!=nil{req.Header.Set("Content-Type","application/json")}
	resp,err:=c.http.Do(req)
	if err!=nil{return fmt.Errorf("%w: transport error",ErrUnavailable)}
	defer resp.Body.Close()
	if resp.StatusCode==http.StatusUnauthorized || resp.StatusCode==http.StatusForbidden || resp.StatusCode==http.StatusNotFound {
		return fmt.Errorf("%w (status %d)",ErrRevoked,resp.StatusCode)
	}
	if resp.StatusCode<200 || resp.StatusCode>=300 {
		return fmt.Errorf("%w (status %d)",ErrUnavailable,resp.StatusCode)
	}
	if target==nil{return nil}
	data,err:=io.ReadAll(io.LimitReader(resp.Body,maxBody+1))
	if err!=nil || len(data)>maxBody{return fmt.Errorf("%w: oversized or unreadable response",ErrIncomplete)}
	if err:=json.Unmarshal(data,target);err!=nil{return fmt.Errorf("%w: invalid response",ErrIncomplete)}
	return nil
}

func (c *Client) appRequest(ctx context.Context,path string,target any) error {
	jwt,err:=c.appJWT()
	if err!=nil{return err}
	return c.request(ctx,http.MethodGet,path,jwt,nil,target)
}

func (c *Client) token(ctx context.Context,id int64) (string,error) {
	if id<=0{return "",ErrRevoked}
	c.mu.Lock()
	defer c.mu.Unlock()
	if cached,ok:=c.cache[id];ok && c.now().Add(5*time.Minute).Before(cached.expires) {
		return cached.token,nil
	}
	jwt,err:=c.appJWT()
	if err!=nil{return "",err}
	var response struct {
		Token string `json:"token"`
		ExpiresAt time.Time `json:"expires_at"`
	}
	path:="/app/installations/"+strconv.FormatInt(id,10)+"/access_tokens"
	err=c.request(ctx,http.MethodPost,path,jwt,map[string]any{"permissions":map[string]string{"metadata":"read"}},&response)
	if err!=nil{
		delete(c.cache,id)
		return "",err
	}
	if len(response.Token)<16 || !response.ExpiresAt.After(c.now().Add(5*time.Minute)) {
		return "",fmt.Errorf("%w: unusable installation token",ErrUnavailable)
	}
	c.cache[id]=cacheEntry{token:response.Token,expires:response.ExpiresAt}
	return response.Token,nil
}

func (c *Client) Installations(ctx context.Context) ([]integration.Installation,error) {
	result:=make([]integration.Installation,0)
	seen:=map[int64]bool{}
	for page:=1;page<=maxPages;page++{
		var batch []struct {
			ID int64 `json:"id"`
			Account struct {Login string `json:"login"`} `json:"account"`
			TargetType string `json:"target_type"`
		}
		if err:=c.appRequest(ctx,fmt.Sprintf("/app/installations?per_page=100&page=%d",page),&batch);err!=nil{return nil,err}
		if len(batch)>100{return nil,ErrIncomplete}
		for _,item:=range batch {
			if item.ID<=0 || item.Account.Login=="" || seen[item.ID] {return nil,ErrIncomplete}
			seen[item.ID]=true
			result=append(result,integration.Installation{ID:strconv.FormatInt(item.ID,10),Account:item.Account.Login,Kind:item.TargetType})
		}
		if len(batch)<100{return result,nil}
	}
	return nil,ErrIncomplete
}

func (c *Client) Resources(ctx context.Context,rawID string) ([]integration.Resource,error) {
	installationID,err:=strconv.ParseInt(rawID,10,64)
	if err!=nil || installationID<=0{return nil,ErrRevoked}
	token,err:=c.token(ctx,installationID)
	if err!=nil{return nil,err}
	result:=make([]integration.Resource,0)
	seen:=map[int64]bool{}
	for page:=1;page<=maxPages;page++{
		var batch struct {
			TotalCount int `json:"total_count"`
			Repositories []struct {
				ID int64 `json:"id"`
				FullName string `json:"full_name"`
				HTMLURL string `json:"html_url"`
			} `json:"repositories"`
		}
		path:=fmt.Sprintf("/installation/repositories?per_page=100&page=%d",page)
		if err:=c.request(ctx,http.MethodGet,path,token,nil,&batch);err!=nil {
			if errors.Is(err,ErrRevoked){c.Invalidate(rawID)}
			return nil,err
		}
		if len(batch.Repositories)>100 || batch.TotalCount<0 || batch.TotalCount>maxPages*100{return nil,ErrIncomplete}
		for _,item:=range batch.Repositories {
			u,err:=url.Parse(item.HTMLURL)
			if err!=nil || item.ID<=0 || item.FullName=="" || len(item.FullName)>200 || u.Scheme!="https" || !strings.EqualFold(u.Hostname(),"github.com") || u.User!=nil || u.RawQuery!="" || u.Fragment!="" || seen[item.ID] {
				return nil,ErrIncomplete
			}
			seen[item.ID]=true
			result=append(result,integration.Resource{ID:strconv.FormatInt(item.ID,10),Name:item.FullName,URL:item.HTMLURL})
		}
		if len(result)>batch.TotalCount{return nil,ErrIncomplete}
		if len(result)==batch.TotalCount{return result,nil}
		if len(batch.Repositories)<100{return nil,ErrIncomplete}
	}
	return nil,ErrIncomplete
}

func (c *Client) Revoke(ctx context.Context,rawID string) error {
	id,err:=strconv.ParseInt(rawID,10,64)
	if err!=nil || id<=0{return ErrRevoked}
	c.mu.Lock()
	cached,ok:=c.cache[id]
	delete(c.cache,id)
	c.mu.Unlock()
	if !ok || !c.now().Before(cached.expires) {
		return nil
	}
	err=c.request(ctx,http.MethodDelete,"/installation/token",cached.token,nil,nil)
	if errors.Is(err,ErrRevoked) {return nil}
	return err
}
