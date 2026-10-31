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

// ImportImage brings a Zeabur service image into Docker. Output is the docker ref to deploy.
//
//   - Zeabur-built images (Pull=false) are exported from k3s' containerd and tagged
//     atoll-import/<service_id>:<digest12>.
//   - Public registry images (Pull=true) are pulled by their original ref, tag kept, so the
//     service keeps following the registry. When the pull fails (rate limit, registry down,
//     image deleted) the cached copy is exported from containerd and tagged with that same
//     ref, which the deploy then uses (it falls back to a local image when a pull fails).
func (t *Takeover) ImportImage(ctx context.Context, p api.ZeaburImagePayload) (string, error) {
	if p.Image == "" || p.ServiceID == "" {
		return "", errors.New("image and service_id are required")
	}
	if strings.ContainsAny(p.ServiceID, "/: ") {
		return "", fmt.Errorf("invalid service id %q", p.ServiceID)
	}
	ref := p.Image
	if p.Pull {
		perr := t.RT.PullImage(ctx, ref, nil)
		if perr == nil {
			return ref, nil
		}
		t.Log.Warn("pull failed; exporting the image from k3s' containerd instead", "image", ref, "err", perr)
		if _, err := t.exportImage(ctx, ref, func(string) string { return ref }); err != nil {
			return "", fmt.Errorf("pull %s: %v; containerd fallback: %w", ref, perr, err)
		}
		return ref, nil
	}
	return t.exportImage(ctx, ref, func(digest string) string { return LocalTag(p.ServiceID, digest) })
}

// exportImage loads ref from k3s' containerd into Docker and tags it target(digest).
func (t *Takeover) exportImage(ctx context.Context, ref string, target func(digest string) string) (string, error) {
	bin, err := t.k3sBin()
	if err != nil {
		return "", err
	}
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
	tgt := target(digest)
	if tgt != ref {
		if _, err := t.RT.ImageInfo(ctx, tgt); err == nil {
			return tgt, nil // already imported (repeat run)
		}
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
	if len(refs) == 0 {
		return "", fmt.Errorf("docker load %s: no image loaded", ref)
	}
	src := refs[0]
	for _, r := range refs {
		if r == ref {
			src = r
		}
	}
	if src != tgt {
		if err := t.RT.TagImage(ctx, src, tgt); err != nil {
			return "", fmt.Errorf("tag %s as %s: %w", src, tgt, err)
		}
	}
	t.Log.Info("imported zeabur image", "image", ref, "local", tgt)
	return tgt, nil
}
