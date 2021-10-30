package limiter

import (
	"context"
	"time"
)

type UnlimitedLimiter struct {
}

func NewUnlimitedLimiter() *UnlimitedLimiter {
	return &UnlimitedLimiter{}
}

type UnlimitedLimiterReservation struct {
	ctx context.Context
}

func (u *UnlimitedLimiter) Wait() <-chan bool {
	m := u.Reserve(context.Background(), nil)
	return m.Wait()
}

func (u *UnlimitedLimiter) Reserve(ctx context.Context, options interface{}) Reservation {
	return &UnlimitedLimiterReservation{
		ctx: ctx,
	}
}

//cancels the reservation.
func (ulr *UnlimitedLimiterReservation) Cancel() {
	//no-op as unlimited doesent require cancelations
}

//Delay returns the delay from now before the reservation completes.
func (ulr *UnlimitedLimiterReservation) Delay() time.Duration {
	return 0
}

//Ok returns if the reservation has reached its done state.
func (ulr *UnlimitedLimiterReservation) Ok() bool {
	return true
}

func (u *UnlimitedLimiterReservation) Wait() <-chan bool {
	m := make(chan bool)
	go func() {
		select {
		case <-u.ctx.Done():
		case m <- true:
		}
		close(m)
	}()
	return m
}
