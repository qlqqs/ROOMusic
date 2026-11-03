package playback

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/qlqq/roomusic/backend/internal/library"
	"github.com/qlqq/roomusic/backend/internal/playback/derivation"
)

// DefaultPreflightWait 是预检等待派生完成的最长时间：超过它返回 ErrPreparing（HTTP 202），客户端立即再次预检。
const DefaultPreflightWait = 20 * time.Second

var (
	// ErrPreparing 表示派生还在进行：预检的等待时间用完仍未完成。调用方应让客户端再次预检（任务继续运行）。
	ErrPreparing = errors.New("playback derivation is still preparing")
	// ErrDerivationNotReady 表示音频请求时派生文件还不存在。音频请求从不启动转换，客户端应先预检。
	ErrDerivationNotReady = errors.New("playback derivation is not ready")
	// ErrDerivationFailed 表示转换失败（ffmpeg 出错、核对不一致、超时或输出超限）；同一个文件短期内的重复请求直接得到它。
	ErrDerivationFailed = errors.New("playback derivation failed")
	// ErrDerivationBusy 表示服务器正忙于转换（并发、排队、单成员等待或磁盘空间的上限），或正在关停；稍后重试。
	ErrDerivationBusy = errors.New("playback derivation is busy")
)

// Derivation 是派生缓存与转换任务的窄端口；生产实现是 *derivation.Manager，测试可以用假实现。
type Derivation interface {
	// Key 返回源文件版本对应的缓存键。
	Key(derivation.SourceVersion) string
	// Await 确保派生文件存在：命中立即返回；否则启动或加入转换，并把调用方登记为等待者，直到完成或 ctx 结束。
	Await(context.Context, derivation.Request) (derivation.Entry, error)
	// OpenEntry 打开缓存中已完成的派生文件，没有时返回 derivation.ErrNotCached；它从不启动转换。
	OpenEntry(key string) (*os.File, int64, error)
}

// Representation 是预检得到的、将要交付给浏览器的表示。它不持有文件。
type Representation struct {
	TrackID string
	// Format 描述交付的表示：Delivery 说明是直出还是派生，MIMEType 与 Codecs 是浏览器拿到的类型。
	Format Format
	// Size 是将要交付的字节数（派生时是派生文件的大小）。
	Size int64
}

// Preflight 做与 Prepare 相同的检查，返回将要交付的表示。直出格式立即返回。派生格式在源文件核对通过后：
// 缓存命中立即返回；未命中则启动或加入转换，并把本请求登记为等待者，最多等待 wait（0 表示 DefaultPreflightWait）：
// 完成返回表示，仍在进行返回 ErrPreparing，失败返回 ErrDerivationFailed，上限已满返回 ErrDerivationBusy。
// ctx 结束（客户端断开）时等待者随之注销，没有等待者且过了宽限期后转换被取消。userID 用于限制每个成员同时等待的转换个数。
// 预检是唯一会启动转换的入口。
func (service *Service) Preflight(ctx context.Context, trackID, userID string, wait time.Duration) (Representation, error) {
	target, err := service.resolve(ctx, trackID)
	if err != nil {
		return Representation{}, err
	}
	if target.format.Delivery == DeliveryOriginal {
		opened, err := target.openSource()
		if err != nil {
			return Representation{}, err
		}
		defer opened.File.Close()
		return Representation{TrackID: target.source.TrackID, Format: target.format, Size: opened.Size}, nil
	}
	if err := target.checkDerivableSource(); err != nil {
		return Representation{}, err
	}
	if wait <= 0 {
		wait = DefaultPreflightWait
	}
	waitCtx, cancel := context.WithTimeout(ctx, wait)
	defer cancel()
	entry, err := service.derivation.Await(waitCtx, derivation.Request{
		Source:  target.derivationSource(),
		User:    userID,
		TrackID: target.source.TrackID,
	})
	if err != nil {
		return Representation{}, classifyDerivation(ctx, err)
	}
	return Representation{TrackID: target.source.TrackID, Format: target.format, Size: entry.Size}, nil
}

// prepareDerived 为音频请求打开已完成的派生文件：先像直出一样核对源文件仍是扫描时的那个（核对后立即关闭源句柄），
// 再按内容版本取缓存。缓存没有时返回 ErrDerivationNotReady，不启动转换。
func (service *Service) prepareDerived(target resolved) (*Prepared, error) {
	if err := target.checkDerivableSource(); err != nil {
		return nil, err
	}
	key := service.derivation.Key(target.derivationVersion())
	file, size, err := service.derivation.OpenEntry(key)
	if errors.Is(err, derivation.ErrNotCached) {
		return nil, ErrDerivationNotReady
	}
	if err != nil {
		return nil, fmt.Errorf("open derived file: %w", err)
	}
	return &Prepared{
		TrackID: target.source.TrackID, Format: target.format,
		File: file, Size: size, ETag: derivedEntityTagValue(target.source.TrackID, key),
	}, nil
}

// checkDerivableSource 核对源文件仍是扫描时的那个（打开、核对大小、修改时间与容器后立即关闭），
// 并要求扫描记录有内容指纹：没有指纹就无法证明转换的是扫描时的内容，需要重新扫描。
func (r resolved) checkDerivableSource() error {
	opened, err := r.openSource()
	if err != nil {
		return err
	}
	_ = opened.File.Close()
	if r.source.ContentFingerprint == "" {
		return fmt.Errorf("%w: scan record has no content fingerprint", ErrSourceChanged)
	}
	return nil
}

// derivationVersion 是缓存键与输出核对使用的源文件版本事实。调用前源文件已通过 openSource，所以大小与修改时间必有。
func (r resolved) derivationVersion() derivation.SourceVersion {
	return derivation.SourceVersion{
		ContentFingerprint: r.source.ContentFingerprint,
		Size:               *r.source.Size,
		ModifiedAt:         *r.source.ModifiedAt,
		SourceKind:         r.source.SourceKind,
		Codec:              r.source.Codec,
		BitDepth:           r.source.BitDepth,
		SampleRate:         r.source.SampleRate,
		Channels:           r.source.Channels,
	}
}

// derivationSource 返回交给转换任务的源文件描述：版本事实，以及任务自己安全打开并核对的回调。
// 回调只捕获本次请求核对过的扫描记录，不借用请求的 context 或句柄，所以不受请求生命周期影响。
func (r resolved) derivationSource() derivation.Source {
	return derivation.Source{
		Version: r.derivationVersion(),
		Open: func(context.Context) (*os.File, error) {
			opened, err := r.openSource()
			if err != nil {
				return nil, err
			}
			return opened.File, nil
		},
		Verify: r.verifyHandle,
	}
}

// verifyHandle 核对已打开的句柄仍是扫描时的那个文件：大小与修改时间等于扫描记录；content 为真时再对句柄重新计算
// 内容指纹并与扫描记录比较（转换前取证，使派生文件不会固化“保留大小与修改时间的原地改写”）。
func (r resolved) verifyHandle(ctx context.Context, file *os.File, content bool) error {
	info, err := file.Stat()
	if err != nil {
		return fmt.Errorf("%w: stat file: %w", ErrSourceUnavailable, err)
	}
	if r.source.Size == nil || r.source.ModifiedAt == nil ||
		info.Size() != *r.source.Size || !info.ModTime().UTC().Truncate(time.Microsecond).Equal(r.source.ModifiedAt.UTC()) {
		return fmt.Errorf("%w: size or modification time differs", ErrSourceChanged)
	}
	if !content {
		return nil
	}
	fingerprint, err := library.ContentFingerprintOf(ctx, filepath.Ext(r.source.RelativePath), file, info.Size())
	if err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return fmt.Errorf("%w: fingerprint: %w", ErrSourceUnavailable, err)
	}
	if fingerprint != r.source.ContentFingerprint {
		return fmt.Errorf("%w: content differs from the scan", ErrSourceChanged)
	}
	return nil
}

// classifyDerivation 把 Await 的错误归为播放层的错误：调用方的 context 已结束（客户端断开）时原样返回它；
// 转换失败（含任务自己的超时）是 ErrDerivationFailed；上限已满或正在关停是 ErrDerivationBusy；
// 等待时间用完（调用方还在）是 ErrPreparing；来源版本问题（ErrSourceChanged、ErrSourceUnavailable）原样透出。
// 必须先判断转换失败再判断超时：任务超时的失败也把 context.DeadlineExceeded 作为原因。
func classifyDerivation(requestCtx context.Context, err error) error {
	var failed *derivation.FailedError
	switch {
	case requestCtx.Err() != nil:
		return requestCtx.Err()
	case errors.As(err, &failed):
		return fmt.Errorf("%w: %s", ErrDerivationFailed, failed.Category)
	case errors.Is(err, derivation.ErrBusy), errors.Is(err, derivation.ErrClosed):
		return ErrDerivationBusy
	case errors.Is(err, context.DeadlineExceeded):
		return ErrPreparing
	default:
		return err
	}
}

// derivedEntityTagValue 返回派生表示的强校验值的不透明部分：SHA-256(derived | 曲目 | 缓存键) 的前 32 位十六进制。
// 缓存键已包含内容版本、转换器版本与转换策略，所以同一个值对应同一份派生字节，并且与原文件的校验值不同。
func derivedEntityTagValue(trackID, key string) string {
	digest := sha256.Sum256([]byte("derived|" + trackID + "|" + key))
	return hex.EncodeToString(digest[:])[:32]
}
