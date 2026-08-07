package gitlab

type tailBuffer struct {
	buf       []byte
	limit     int
	truncated bool
}

func newTailBuffer(limit int) *tailBuffer {
	return &tailBuffer{limit: limit, buf: make([]byte, 0, limit)}
}

func (t *tailBuffer) Write(p []byte) (int, error) {
	n := len(p)
	if t.limit <= 0 {
		t.truncated = t.truncated || n > 0
		return n, nil
	}
	if len(p) >= t.limit {
		t.truncated = t.truncated || len(t.buf) > 0 || len(p) > t.limit
		t.buf = append(t.buf[:0], p[len(p)-t.limit:]...)
		return n, nil
	}
	if len(t.buf)+len(p) > t.limit {
		drop := len(t.buf) + len(p) - t.limit
		copy(t.buf, t.buf[drop:])
		t.buf = t.buf[:len(t.buf)-drop]
		t.truncated = true
	}
	t.buf = append(t.buf, p...)
	return n, nil
}

func (t *tailBuffer) Bytes() []byte {
	return append([]byte(nil), t.buf...)
}
