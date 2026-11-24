package zones

import (
	"io"
)

type pcmReader struct {
	io.Reader
	session RenderSession
}

func (r pcmReader) Close() error {
	r.session.Close()
	return nil
}

// OpenAirPlayPCM 将指定渲染流的交错小端 PCM 交给当前托管输出，关闭 reader 可中断阻塞读取。
func (s *Service) OpenAirPlayPCM(userID, sessionID string, seq int64) (io.ReadCloser, error) {
	out, err := s.findOutput(userID, sessionID)
	if err != nil {
		return nil, err
	}
	z := out.zone
	z.mu.Lock()
	defer z.mu.Unlock()
	h := z.handleFor(&seq)
	if outputError(out) != nil || !out.managed || z.device.Kind != "airplay" || h == nil || h.taken {
		return nil, ErrStreamGone
	}
	h.taken = true
	return pcmReader{Reader: h.session.Body(), session: h.session}, nil
}
