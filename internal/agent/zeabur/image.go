package zeabur

import (
	"context"
	"errors"
	"fmt"
	"io"
	"runtime"
	"strings"

	"github.com/Sakuralaaa/Atoll/internal/api"
	"github.com/Sakuralaaa/Atoll/internal/zeabur/k8s"
)

// LocalTag is the docker tag given to an image exported from k3s' containerd.
func LocalTag(serviceID, digest string) string {
	d := strings.TrimPrefix(digest, "sha256:")
	if len(d) > 12 {
		d = d[:12]
	}
	if d == "" {
		d = "latest"
	}
	return "atoll-import/" + strings.ToLower(serviceID) + ":" + d
}

// CtrDigest finds the manifest digest of ref in `ctr images ls` output.
func CtrDigest(ctrLs, ref string) string {
	for _, line := range strings.Split(ctrLs, "\n") {
		f := strings.Fields(line)
		if len(f) >= 3 && f[0] == ref && strings.HasPrefix(f[2], "sha256:") {
			return f[2]
		}
	}
	return ""
}

// ImportImage exports the image from k3s' containerd and loads it into Docker, tagging it
// atoll-import/<service_id>:<digest12>. Output is that local ref.
func (t *Takeover) ImportImage(ctx context.Context, p api.ZeaburImagePayload) (string, error) {
	if p.Image == "" || p.ServiceID == "" {
		return "", errors.New("image and service_id are required")
	}
	if strings.ContainsAny(p.ServiceID, "/: ") {
		return "", fmt.Errorf("invalid service id %q", p.ServiceID)
	}
	bin, err := t.k3sBin()
	if err != nil {
		return "", err
	}
	ref := p.Image
	ls, err := t.X.Output(ctx, bin, "ctr", "-n", "k8s.io", "images", "ls")
	if err != nil {
		return "", err
	}
	digest := CtrDigest(string(ls), ref)
	if digest == "" {
		// The payload may carry repo@sha256 or a digest-pinned ref; try the digest itself.
		if d := k8s.DigestOf(ref); d != "" {
			digest = d
		}
		if digest == "" || !k8s.ParseCtrImages(string(ls))[ref] && !k8s.ParseCtrImages(string(ls))[digest] {
			return "", fmt.Errorf("image %s is not in k3s' containerd cache (Zeabur may have garbage-collected it)", ref)
		}
	}
	target := LocalTag(p.ServiceID, digest)
	if _, err := t.RT.ImageInfo(ctx, target); err == nil {
		return target, nil // already imported (repeat run)
	}

	pr, pw := io.Pipe()
	exportErr := make(chan error, 1)
	go func() {
		err := t.X.Stream(ctx, nil, pw, bin, "ctr", "-n", "k8s.io", "images", "export",
			"--platform", "linux/"+runtime.GOARCH, "-", ref)
		pw.CloseWithError(err)
		exportErr <- err
	}()
	refs, lerr := t.RT.LoadImage(ctx, pr)
	pr.CloseWithError(errors.New("load finished"))
	if eerr := <-exportErr; eerr != nil {
		return "", fmt.Errorf("ctr export %s: %w", ref, eerr)
	}
	if lerr != nil {
		return "", fmt.Errorf("docker load: %w", lerr)
	}
	src := refs[0]
	for _, r := range refs {
		if r == ref {
			src = r
		}
	}
	if err := t.RT.TagImage(ctx, src, target); err != nil {
		return "", fmt.Errorf("tag %s as %s: %w", src, target, err)
	}
	t.Log.Info("imported zeabur image", "image", ref, "local", target)
	return target, nil
}
