package local

import (
	"context"
	"fmt"
	"sort"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/streemtech/limiter"
)

var endOfTime, _ = time.Parse(time.RFC1123, "Mon, 02 Jan 2025 15:04:05 MST")

//SlidingWindowLimiter is the limiter used in this case for a local sliding window rate limiter
type SlidingWindowLimiter struct {
	width        time.Duration
	slots        int
	mux          *sync.Mutex
	reservations SlidingWindowReservations
	newDelay     chan time.Duration
	nextTrigger  time.Time
}

//NewSlidingWindow returns a limiter that uses redis as a backing.
//The limiter implements a sliding window limiter.
//The sliding window limiter is
func NewSlidingWindow(windowWidth time.Duration, slots int) (*SlidingWindowLimiter, error) {

	l := &SlidingWindowLimiter{
		newDelay:     make(chan time.Duration),
		slots:        slots,
		width:        windowWidth,
		mux:          &sync.Mutex{},
		reservations: make(SlidingWindowReservations, 0),
		nextTrigger:  endOfTime,
	}

	go l.cleanupLoop()

	l.triggerCleanup(0)
	return l, nil
}

type SlidingWindowReservationStatus int

const (
	//the res has been cretaed, but is has not been let through.
	Opened SlidingWindowReservationStatus = iota
	//the reservation was been canceled before it was triggered.
	Canceled
	//the reservation has been told that it is triggered.
	Triggered
)

type SlidingWindowReservation struct {
	limiter *SlidingWindowLimiter
	ctx     context.Context

	//the chanel that is used to wait on.
	waitChan chan bool

	//the time this reservation was created.
	reservedAt time.Time

	//activated at is the time that this reservation was set to complete.
	activatedAt time.Time

	//the number of slots this reservation is taking.
	reservationSize int

	//uuid is the unique string used to make sure that cancel removes the proper window.
	uuid string

	status SlidingWindowReservationStatus
}

//triggerCleanup tells cleanup to run. in time N.
//a new time is passed to the
func (l *SlidingWindowLimiter) triggerCleanup(in time.Duration) {

	l.newDelay <- in
}

func (l *SlidingWindowLimiter) cleanupLoop() {
	//create the largest possible delay.
	nextTrigger := endOfTime

	for {
		select {
		case <-time.After(time.Until(nextTrigger)):
			l.cleanup()
			//reset the next delay to be large.
			nextTrigger = endOfTime
		case d := <-l.newDelay:
			tx := time.Now().Add(d)
			if tx.Before(nextTrigger) {
				nextTrigger = tx
			}
		}
	}
}

//cleanup is an infinite loop that is started and waits for triggers to run.
//it
func (l *SlidingWindowLimiter) cleanup() {
	defer func() {
		r := recover()
		if r != nil {
			fmt.Printf("ERROR: %+v\n", r)
		}
	}()

	l.mux.Lock()

	//sort. find the
	sort.Sort(l.reservations)
	now := time.Now()
	fallOff := time.Now().Add(l.width * -1)
	deleteEndIDX := len(l.reservations) //set to length. Will clear array if all reservations are done..

	//activeList is the list of reservations that are either triggered or open
	activeList := make([]*SlidingWindowReservation, 0)
	// // canceled list is the list of canceled nodes.
	// canceledList := make([]*SlidingWindowReservation, 0)

	//remove all canceled nodes
	for _, res := range l.reservations {
		if res.status == Canceled {
			// canceledList = append(canceledList, res)
		} else {
			activeList = append(activeList, res)
		}
	}

	l.reservations = activeList

	e := time.Time{}
	//find the first reservation that is out of the time range.
	for idx, res := range l.reservations {

		if res.status == Triggered || res.status == Canceled {
			if res.activatedAt.After(fallOff) && res.activatedAt != e {
				// panic("")
				//this is the first node that is to be kept.
				//Delete all nodes before
				deleteEndIDX = idx
				break
			}
		} else {
			//not yet triggered or canceled,
			//remove all triggered/cancled and then go to the next set to change.
			deleteEndIDX = idx
			break
		}
	}
	l.reservations = l.reservations[deleteEndIDX:]

	count := 0
	for _, res := range l.reservations {
		if count >= l.slots {
			break
		}

		if res.status == Opened {
			res.status = Triggered
			res.activatedAt = now
			close(res.waitChan)
		}
		count++
	}

	if len(l.reservations) > 0 {
		f := l.reservations[0]
		falloff := f.activatedAt.Add(l.width)
		s := falloff.Sub(now)
		go func(sleep time.Duration) {
			l.triggerCleanup(sleep)
		}(s)
	} else {
		l.nextTrigger = endOfTime
	}
	//todo set when to start next

	l.mux.Unlock()

}

func (l *SlidingWindowLimiter) Wait() <-chan bool {
	m := l.Reserve(context.Background(), nil)
	// repr.Println(l)
	return m.Wait()
}

func (l *SlidingWindowLimiter) Reserve(ctx context.Context, options interface{}) limiter.Reservation {
	//create reservation.
	t := time.Now()
	c := make(chan bool)
	s := uuid.NewString()
	r := &SlidingWindowReservation{
		ctx:             ctx,
		limiter:         l,
		reservedAt:      t,
		reservationSize: 1,
		waitChan:        c,
		uuid:            s,
		status:          Opened,
	}

	//add reservation
	l.mux.Lock()
	l.reservations = append(l.reservations, r)
	l.mux.Unlock()
	go l.triggerCleanup(0)
	return r
}

//cancels the reservation.
func (r *SlidingWindowReservation) Cancel() {
	r.limiter.mux.Lock()
	r.status = Canceled
	close(r.waitChan)
	r.limiter.mux.Unlock()
}

//Delay returns the delay from now before the reservation completes.
func (r *SlidingWindowReservation) Delay() time.Duration {
	//it should be possible to return the expected delay for calculations.
	panic("SlidingWindowReservation does not implement Delay")
}

//Ok returns if the reservation has reached its done state.
//if canceled, it returns false.
func (r *SlidingWindowReservation) Ok() bool {
	return r.status == Triggered
}

func (r *SlidingWindowReservation) Wait() <-chan bool {

	//start function that will close the waitChan if the context is closed.
	go func() {
		<-r.ctx.Done()
		close(r.waitChan)
	}()
	return r.waitChan
}

type SlidingWindowReservations []*SlidingWindowReservation

func (r SlidingWindowReservations) Len() int { return len(r) }
func (r SlidingWindowReservations) Less(i, j int) bool {
	ri := r[j]
	rj := r[j]
	//sort by reservation time.
	//then by the size of the reservation
	if ri.reservedAt.Equal(rj.reservedAt) {
		return ri.reservationSize < rj.reservationSize
	}
	return ri.reservedAt.Before(rj.reservedAt)
}
func (r SlidingWindowReservations) Swap(i, j int) { r[i], r[j] = r[j], r[i] }
