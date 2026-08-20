package admission

type Limiter struct{ slots chan struct{} }

func New(limit int) *Limiter {
	if limit < 1 {
		limit = 1
	}
	return &Limiter{slots: make(chan struct{}, limit)}
}

func (l *Limiter) Acquire() (func(), bool) {
	select {
	case l.slots <- struct{}{}:
		return func() { <-l.slots }, true
	default:
		return nil, false
	}
}

func (l *Limiter) InFlight() int { return len(l.slots) }
