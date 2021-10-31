package redis

import (
	"context"
	"fmt"
	"strconv"
	"sync"
	"time"

	redis "github.com/go-redis/redis/v8"
	"github.com/go-redsync/redsync/v4"
	"github.com/go-redsync/redsync/v4/redis/goredis/v8"
	"github.com/google/uuid"
	"github.com/streemtech/limiter"
)

type SlidingWindowReservationStatus int

const (
	//the res has been cretaed, but is has not been let through.
	Opened SlidingWindowReservationStatus = iota
	//the reservation was been canceled before it was triggered.
	Canceled
	//the reservation has been told that it is triggered.
	Triggered
)

type SlidingWindowLimiter struct {
	redis *redis.Client

	//key is the base used for all other keys
	key string

	//lockKey is the key that holds the lock
	lockKey string

	//windowKey is the key that stores where the window is.
	windowKey string

	mux  *sync.Mutex
	uuid string

	redsync *redsync.Redsync
	redmux  *redsync.Mutex
	//windowSize is the size of the window. IE the number of elements that can be contained in a single span N.
	windowSize int

	//windowTime is the time between keys getting triggered.
	windowTime time.Duration
	//tje context that can be canceled to stop the sliding window updater.
	ctx context.Context
}

//NewSlidingWindow returns a limiter that uses redis as a backing.
//The limiter implements a sliding window limiter.
//The sliding window limiter is
func NewSlidingWindow(ctx context.Context, redis *redis.Client, masterKey string, window time.Duration, slots int) (*SlidingWindowLimiter, error) {

	//TODO add timeout for window and mutex,
	//and add way for them to self refresh so long as the limiter is alive.

	//set the UUID.
	UUID := uuid.NewString()

	_, err := redis.Ping(context.TODO()).Result()
	if err != nil {
		return nil, fmt.Errorf("error pinging redis: %w", err)
	}

	d := &SlidingWindowLimiter{
		redis:      redis,
		ctx:        ctx,
		key:        masterKey + ":__meta__",
		lockKey:    masterKey + ":__meta__:mutex",
		windowKey:  masterKey + ":__meta__:window",
		mux:        &sync.Mutex{},
		uuid:       UUID,
		redsync:    redsync.New(goredis.NewPool(redis)),
		windowSize: slots,
		windowTime: window,
	}

	go func() {
		for {
			d.timeout()
			select {
			case <-ctx.Done():
				return
			case <-time.After(time.Minute * 5):
			}
		}
	}()

	d.redmux = d.redsync.NewMutex(d.lockKey, redsync.WithExpiry(time.Second))

	return d, nil
}

//timeout expires the window key so that, at worst. it is an hour left.
//this allows for the keeping clean of the redis database.
func (u *SlidingWindowLimiter) timeout() {
	u.redis.Expire(context.TODO(), u.windowKey, time.Hour).Result()
}

type SlidingWindowReservation struct {
	limiter *SlidingWindowLimiter
	ctx     context.Context
	id      string
	timeout time.Time
	cancel  func()
	status  SlidingWindowReservationStatus
}

func (u *SlidingWindowLimiter) Wait() <-chan bool {
	m := u.Reserve(context.Background(), nil)
	return m.Wait()
}

func (u *SlidingWindowLimiter) Reserve(ctx context.Context, options interface{}) limiter.Reservation {
	cx, canc := context.WithCancel(ctx)
	r := &SlidingWindowReservation{
		ctx:     cx,
		cancel:  canc,
		limiter: u,
		id:      uuid.NewString(),
	}
	r.timeout = u.createReservation(r.id)
	u.timeout()
	return r
}

//cancels the reservation.
func (ulr *SlidingWindowReservation) Cancel() {
	//only able to close reservations that have not been closed before, and are not yet triggered.
	if ulr.status != Opened {
		return
	}
	ulr.status = Canceled
	ulr.limiter.removeReservation(ulr)
	ulr.cancel()

}

//Delay returns the delay from now before the reservation completes. (set during init)
func (ulr *SlidingWindowReservation) Delay() time.Duration {
	return time.Until(ulr.timeout)
}

//Ok returns if the reservation has reached its done state.
func (ulr *SlidingWindowReservation) Ok() bool {
	return ulr.status == Triggered
}

func (ulr *SlidingWindowReservation) Wait() <-chan bool {
	m := make(chan bool)
	go func() {
		select {
		case <-ulr.ctx.Done():
		case <-time.After(ulr.Delay()):
			ulr.status = Triggered
		}
		close(m)
	}()
	return m
}

//removeReservation removes a reservation from the list, if the timer has not yet passed.
func (u *SlidingWindowLimiter) removeReservation(ulr *SlidingWindowReservation) {
	if ulr.timeout.Before(time.Now()) {
		//the timeout is before now. Nothing to do. Should assume triggered.
		return
	}
	u.redis.ZRem(context.TODO(), u.windowKey, ulr.id)

}

//creates a reservation in the limiter based on the ID.
//the time returned, if not 0, is the time that the reservation triggers. So long as the time is in the future, it can be canceled.
func (u *SlidingWindowLimiter) createReservation(id string) time.Time {
	u.lock()
	defer u.unlock()
	checkTime := time.Now()
	end := checkTime.Add(-1 * u.windowTime).UnixMilli()
	//remove all data from the set if it is timed out.
	remKey := "(" + strconv.Itoa(int(end))
	u.redis.ZRemRangeByScore(context.TODO(), u.windowKey, "0", remKey).Val()

	//get the length of the number of items in the queue RN.
	length, err := u.redis.ZCard(context.TODO(), u.windowKey).Result()
	if err != nil {
		return time.Time{}
	}
	//if there are less items than the window size, just return the current time as the trigger time.
	if int(length) < u.windowSize {
		u.redis.ZAdd(context.TODO(), u.windowKey, &redis.Z{
			Score:  float64(checkTime.UnixMilli()),
			Member: id,
		})
		return checkTime
	}

	//if there are MORE items, get the time of the n-1th item, add the window size, insert that, and return the inserted time.

	idx := int64(u.windowSize) * -1
	res, err := u.redis.ZRange(context.TODO(), u.windowKey, idx, idx).Result()
	if err != nil || len(res) != 1 {
		return time.Time{}
	}

	lastID := res[0]

	score, err := u.redis.ZScore(context.TODO(), u.windowKey, lastID).Result()
	if err != nil || len(res) != 1 {
		return time.Time{}
	}
	newTime := time.UnixMilli(int64(score)).Add(u.windowTime)
	u.redis.ZAdd(context.TODO(), u.windowKey, &redis.Z{
		Score:  float64(newTime.UnixMilli()),
		Member: id,
	})
	return newTime
}

//lock will attempt to gain a lock over u.lockKey
func (u *SlidingWindowLimiter) lock() (err error) {
	for i := 0; i < 10; i++ {
		err = u.redmux.Lock()
		if err != nil {
			continue
		}
		return nil
	}
	return err
}

//unlockFor will remove the lock.
func (u *SlidingWindowLimiter) unlock() (err error) {
	_, err = u.redmux.Unlock()
	return err

}
