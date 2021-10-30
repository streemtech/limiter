package limiter

import (
	"context"
	"time"
)

//Waiter is the interface defining a rate limiter.
type Waiter interface {
	//Wait returns a channel that only returns when the specified number of tokens is avaliable.
	//the boolean returned is true if the limit is allowed to proceed. If false,
	//the limit was, canceled, will not fit, or encountered some other error.
	//In the case of the context being done, the the limit not
	Wait() <-chan bool
}

type Limiter interface {
	//Reservation is a limiter reservation. Reservations are pre-emptive limits
	Reserve(ctx context.Context, options interface{}) Reservation
	Waiter
}

type Reservation interface {
	//cancels the reservation.
	Cancel()

	//Delay returns the delay from now before the reservation completes.
	Delay() time.Duration

	//Ok returns if the reservation has reached its done state.
	Ok() bool

	Waiter
}
