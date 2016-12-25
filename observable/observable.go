package grx

import (
	"sync"
	"time"

	"github.com/jochasinga/grx/bang"
	"github.com/jochasinga/grx/observer"
	"github.com/jochasinga/grx/stream"
	"github.com/jochasinga/grx/subject"
)

// Observable is a stream of Emitters
type Observable struct {
	stream.EventStream
	notifier *bang.Notifier
	observer *subject.Subject
}

// DefaultObservable is a default Observable used by the constructor New.
// It is preferable to using the new keyword to create one.
var DefaultObservable = func() *Observable {
	o := &Observable{
		EventStream: eventstream.New(),
		notifier:    bang.New(),
	}
	o.observer = subject.New(func(s *subject.Subject) {
		s.Observable = o
	})
	return o
}()

func (o *Observable) Done() {
	o.notifier.Done()
}

func (o *Observable) Unsubscribe() {
	o.notifier.Unsubscribe()
}

// New returns a new pointer to a default Observable.
func New(fs ...func(*Observable)) *Observable {
	o := DefaultObservable
	if len(fs) > 0 {
		for _, f := range fs {
			f(o)
		}
	}
	return o
}

// Create creates a new Observable provided by one or more function that takes an Observer as an argument
func Create(f func(*Observer), fs ...func(*Observer)) *Observable {
	o := New()
	fs = append([]func(*Observer){f}, fs...)
	go func() {
		for _, f := range fs {
			f(o)
		}
	}()
	return o
}

// Add adds an item to the Observable and returns that Observable.
// If the Observable is done, it creates a new one and return it.
func (o *Observable) Add(e Emitter, es ...Emitter) *Observable {
	es = append([]Emitter{e}, es...)
	out := New()
	if !o.isDone() {
		go func() {
			for _, e := range es {
				o.EventStream <- e
			}
		}()
		*o = *out
		return out
	}
	go func() {
		for _, e := range es {
			out.EventStream <- e
		}
	}()
	return out
}

// Empty creates an Observable with one last item marked as "completed".
func Empty() *Observable {
	o := New()
	go func() {
		o.Done()
	}()
	return o
}

// Interval creates an Observable emitting incremental integers infinitely
// between each given time interval.
func Interval(d time.Duration) *Observable {
	o := New()
	go func() {
		i := 0
		for {
			o.EventStream <- i
			<-time.After(d)
			i++
		}
	}()
	return o
}

// Range creates an Observable that emits a particular range of sequential integers.
func Range(start, end int) *Observable {
	o := New()
	go func() {
		for i := start; i < end; i++ {
			o.EventStream <- i
		}
		o.Done()
	}()
	return o
}

// Just creates an observable with only one item and emit "as-is".
// source := observable.Just("https://someurl.com/api")
func Just(em Emitter, ems ...Emitter) Observable {
	o := new(Observable)
	emitters := append([]Emitter{em}, ems...)

	go func() {
		for _, emitter := range emitters {
			o.EventStream <- emitter
		}
		o.Done()
	}()
	return o
}

// From creates an Observable from an Iterator
func From(iter Iterator) *Observable {
	o := New()
	go func() {
		for iter.HasNext() {
			o.EventStream <- iter.Next()
		}
		o.Done()
	}()
	return o
}

// Start creates an Observable from one or more directive-like functions
func Start(f func() Emitter, fs ...func() Emitter) *Observable {
	o := New()
	fs = append([](func() Emitter){f}, fs...)

	var wg sync.WaitGroup
	wg.Add(len(fs))
	for _, f := range fs {
		go func(f func() Emitter) {
			o.EventStream <- f()
			wg.Done()
		}(f)
	}
	go func() {
		wg.Wait()
		o.Done()
	}()
	return o
}

func processStream(o *Observable, ob *Observer, async bool) {
	async = false
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		for emitter := range o.EventStream {
			item, err := emitter.Emit()
			if err != nil {
				if item == nil {
					if ob.ErrHandler != nil {
						ob.ErrHandler(err)
					}
					return
				}
			} else {
				if item != nil {
					if ob.NextHandler != nil {
						ob.NextHandler(item)
					}
				}
			}
		}
		wg.Done()
	}()
	
	go func() {
		wg.Wait()
		o.Done()
	}()

	select {
	case <-o.unsubscribed:
		return
	case <-o.done:
		if ob.DoneHandler != nil {
			ob.DoneHandler()
			return
		}
	}
}

func checkObservable(o *Observable) error {

	switch {
	case o == nil:
		return NewError(grx.NilObservableError)
	case o.EventStream == nil:
		return eventstream.NewError(grx.NilEventStreamError)
	case o.isDone():
		return NewError(grx.EndOfIteratorError)
	default:
		return nil
	}
	return nil
}
	
/*
// SubscribeWith subscribes handlers to the Observable and starts it.
//func (o *Observable) SubscribeFunc(nxtf func(v interface{}), errf func(e error), donef func()) (Subscriptor, error) {
func (o *BaseObservable) SubscribeFunc(nxtf func(v interface{}), errf func(e error), donef func()) (Subscriptor, error) {

	err := checkObservable(o)
	if err != nil {
		return nil, err
	}

	ob := Observer(&BaseObserver{
		NextHandler: NextFunc(nxtf),
		ErrHandler:  ErrFunc(errf),
		DoneHandler: DoneFunc(donef),
	})

	o.runStream(ob)

	return Subscriptor(&Subscription{SubscribeAt: time.Now()}), nil
}
*/

// SubscribeHandler subscribes a Handler to the Observable and starts it.
func (o *BaseObservable) Subscribe(h EventHandler) (Subscriptor, error) {
	err := checkObservable(o)
	if err != nil {
		return nil, err
	}

	ob := observer.New()

	var (
		nextf NextFunc
		errf  ErrFunc
		donef DoneFunc
	)

	isObserver := false

	switch h := h.(type) {
	case NextFunc:
		nextf = h
	case ErrFunc:
		errf = h
	case DoneFunc:
		donef = h
	case *BaseObserver:
		ob = h
		isObserver = true
	}

	if !isObserver {
		ob = observer.New(func(ob *Observer) {
			ob.NextHandler: nextf,
			ob.ErrHandler: errf,
			ob.DoneHandler: donef,
		})
	}

	o.runStream(ob)

	/*
		handlers := append([]EventHandler{h}, hs...)

		nc, errc, dc := make(chan NextFunc), make(chan ErrFunc), make(chan DoneFunc)

		var wg sync.WaitGroup
		for _, handler := range handlers {
			wg.Add(1)
			go func() {
				switch handler := handler.(type) {
				case NextFunc:
					nc <- handler
				case ErrFunc:
					errc <- handler
				case DoneFunc:
					dc <- handler
				case *BaseObserver:
					switch {
					case handler.NextHandler != nil:
						nc <- handler.NextHandler
					case handler.ErrHandler != nil:
						errc <- handler.ErrHandler
					case handler.DoneHandler != nil:
						dc <- handler.DoneHandler
					}
				}
				wg.Done()
			}()
		}

		go func() {
			wg.Wait()
			close(nc)
			close(errc)
			close(dc)
		}()

		//var wg sync.WaitGroup
		wg.Add(1)

		go func(stream chan interface{}) {
			for item := range stream {
				switch v := item.(type) {
				case error:
					if fn, ok := <-errc; ok {
						fn(v)
					}
					return
				case interface{}:
					if fn, ok := <-nc; ok {
						fn(v)
					}
				}
			}
			wg.Done()
		}(o.C)

		go func() {
			wg.Wait()
			if fn, ok := <-dc; ok {
				o.done <- struct{}{}
				fn()
			}
		}()
	*/
	return Subscriptor(&Subscription{SubscribeAt: time.Now()}), nil
}