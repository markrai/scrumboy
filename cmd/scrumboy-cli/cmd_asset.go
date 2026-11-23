package main

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"net/textproto"
	"net/url"
	"os"
	"os/signal"
	"path/filepath"
	"regexp"
	"strings"
)

const assetUsage = `asset <get|put|copy>

  get <id|url> [-o FILE|-]            download an inline image (default FILE: <id>.<ext>)
  put <file> -p slug [--todo N]       upload an image to a project; prints its /api/assets/<id> URL
  copy <id|url> -p slug [--todo N]    download an image and upload it to another project

Images belong to one project, so moving a todo to another board means copying
its images with 'asset copy' and pointing the body at the new URLs. <id> is the
UUID, or any link containing it (/api/assets/<id>, a full URL).
`

var assetUUIDRe = regexp.MustCompile(`[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}`)

// maxAssetFileBytes bounds what the CLI reads for an upload. The server's own
// limit (10 MiB by default) is authoritative; this only stops a wrong file.
const maxAssetFileBytes = 32 << 20

func cmdAsset(a *app, args []string) error {
	if len(args) == 0 {
		fmt.Fprint(a.err, assetUsage)
		return usagef("missing asset subcommand")
	}
	switch args[0] {
	case "-h", "--help", "help":
		fmt.Fprint(a.out, assetUsage)
		return nil
	case "get":
		return assetGet(a, args[1:])
	case "put":
		return assetPut(a, args[1:])
	case "copy":
		return assetCopy(a, args[1:])
	}
	return usagef("unknown asset subcommand %q", args[0])
}

func assetID(ref string) (string, error) {
	// Only look at the path, so a UUID in a query string or host never matches.
	if u, err := url.Parse(ref); err == nil && u.Path != "" {
		ref = u.Path
	}
	id := assetUUIDRe.FindString(strings.ToLower(ref))
	if id == "" {
		return "", usagef("%q does not contain an asset id", ref)
	}
	return id, nil
}

func assetExt(contentType string) string {
	mt, _, _ := mime.ParseMediaType(contentType)
	switch mt {
	case "image/png":
		return ".png"
	case "image/jpeg":
		return ".jpg"
	case "image/gif":
		return ".gif"
	}
	return ".bin"
}

func (c *cmdCtx) rawClient() (*Client, context.Context, func(), error) {
	cl, err := c.client()
	if err != nil {
		return nil, nil, nil, err
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	return cl, ctx, stop, nil
}

func (c *cmdCtx) downloadAsset(id string) ([]byte, string, error) {
	cl, ctx, stop, err := c.rawClient()
	if err != nil {
		return nil, "", err
	}
	defer stop()
	hdr, data, err := cl.Raw(ctx, http.MethodGet, "/mcp/assets/"+id, "", nil)
	if err != nil {
		return nil, "", err
	}
	return data, hdr.Get("Content-Type"), nil
}

type uploadedAsset struct {
	AssetID   string `json:"assetId"`
	URL       string `json:"url"`
	MimeType  string `json:"mimeType"`
	SizeBytes int64  `json:"sizeBytes"`
	Width     int    `json:"width"`
	Height    int    `json:"height"`
	Filename  string `json:"filename"`
}

func (c *cmdCtx) uploadAsset(slug string, todo int64, name string, data []byte) (*uploadedAsset, error) {
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	h := make(textproto.MIMEHeader)
	h.Set("Content-Disposition", fmt.Sprintf(`form-data; name="file"; filename=%q`, filepath.Base(name)))
	h.Set("Content-Type", "application/octet-stream")
	part, err := mw.CreatePart(h)
	if err != nil {
		return nil, err
	}
	if _, err := part.Write(data); err != nil {
		return nil, err
	}
	if err := mw.Close(); err != nil {
		return nil, err
	}
	path := "/mcp/assets/" + url.PathEscape(slug)
	if todo > 0 {
		path += fmt.Sprintf("?todo=%d", todo)
	}
	cl, ctx, stop, err := c.rawClient()
	if err != nil {
		return nil, err
	}
	defer stop()
	_, body, err := cl.Raw(ctx, http.MethodPost, path, mw.FormDataContentType(), buf.Bytes())
	if err != nil {
		return nil, err
	}
	var out uploadedAsset
	if err := json.Unmarshal(body, &out); err != nil || out.URL == "" {
		return nil, fmt.Errorf("unexpected upload response: %s", snippet(body))
	}
	return &out, nil
}

func (c *cmdCtx) printUploaded(u *uploadedAsset) error {
	if c.json {
		return c.printJSON(u)
	}
	fmt.Fprintln(c.out, u.URL)
	return nil
}

func assetGet(a *app, args []string) error {
	var outPath string
	c, err := a.parse("asset get", "asset get <id|url> [-o FILE|-]", args, func(fs *flag.FlagSet) {
		fs.StringVar(&outPath, "o", "", "output file, or - for stdout (default <id>.<ext>)")
	})
	if err != nil {
		return err
	}
	if err := c.wantPos(1, "an asset id or URL"); err != nil {
		return err
	}
	id, err := assetID(c.pos[0])
	if err != nil {
		return err
	}
	data, ct, err := c.downloadAsset(id)
	if err != nil {
		return err
	}
	if outPath == "-" {
		_, err := c.out.Write(data)
		return err
	}
	if outPath == "" {
		outPath = id + assetExt(ct)
	}
	// O_EXCL: never overwrite a file that is already there.
	f, err := os.OpenFile(outPath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	if _, err := f.Write(data); err != nil {
		f.Close()
		os.Remove(outPath) // do not leave a partial file that blocks a retry
		return err
	}
	if err := f.Close(); err != nil {
		os.Remove(outPath)
		return err
	}
	if c.json {
		return c.printJSON(map[string]any{"file": outPath, "bytes": len(data), "mimeType": ct})
	}
	fmt.Fprintln(c.out, outPath)
	return nil
}

func assetPut(a *app, args []string) error {
	var todo int64
	c, err := a.parse("asset put", "asset put <file> -p slug [--todo N]", args, func(fs *flag.FlagSet) {
		fs.Int64Var(&todo, "todo", 0, "attach to this todo's local id (needed for contributors)")
	})
	if err != nil {
		return err
	}
	if err := c.wantPos(1, "an image file"); err != nil {
		return err
	}
	slug, err := c.requireProject()
	if err != nil {
		return err
	}
	f, err := os.Open(c.pos[0])
	if err != nil {
		return err
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, maxAssetFileBytes+1))
	if err != nil {
		return err
	}
	if len(data) > maxAssetFileBytes {
		return fmt.Errorf("%s is larger than %d MiB", c.pos[0], maxAssetFileBytes>>20)
	}
	up, err := c.uploadAsset(slug, todo, c.pos[0], data)
	if err != nil {
		return err
	}
	return c.printUploaded(up)
}

func assetCopy(a *app, args []string) error {
	var todo int64
	c, err := a.parse("asset copy", "asset copy <id|url> -p slug [--todo N]", args, func(fs *flag.FlagSet) {
		fs.Int64Var(&todo, "todo", 0, "attach to this todo's local id in the target project")
	})
	if err != nil {
		return err
	}
	if err := c.wantPos(1, "an asset id or URL"); err != nil {
		return err
	}
	slug, err := c.requireProject()
	if err != nil {
		return err
	}
	id, err := assetID(c.pos[0])
	if err != nil {
		return err
	}
	data, ct, err := c.downloadAsset(id)
	if err != nil {
		return err
	}
	up, err := c.uploadAsset(slug, todo, id+assetExt(ct), data)
	if err != nil {
		return err
	}
	return c.printUploaded(up)
}
