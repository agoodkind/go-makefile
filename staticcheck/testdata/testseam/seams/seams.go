package seams

import "time"

var (
	Now  = time.Now
	Wait = time.Sleep
)

type clockDependencies struct {
	now func() time.Time // want now:`seam\(func:time.Now\)`
}

func newClockDependencies() clockDependencies {
	return clockDependencies{now: time.Now}
}

func Stamp() time.Time {
	Wait(0)
	return newClockDependencies().now()
}

func realSend(message string) error { return nil }

func realRun() int { return 1 }

func otherRun() int { return 2 }

var Send = realSend

type dependencies struct {
	run func() int // want run:`seam\(func:example.com/testseam/seams.realRun\)`
}

func newDependencies() dependencies {
	return dependencies{run: realRun}
}

func Result() int {
	return newDependencies().run()
}

// Config has one production value for Run.
type Config struct {
	Run func() int // want Run:`seam\(func:example.com/testseam/seams.realRun\)`
}

func Default() Config {
	return Config{Run: realRun}
}

// Callback has two production values for Done.
type Callback struct {
	Done func() int
}

func First() Callback { return Callback{Done: realRun} }

func Second() Callback { return Callback{Done: otherRun} }

// Options has no production value for Now.
type Options struct {
	Now func() int
}

func Use(options Options, callback Callback, config Config) int {
	total := callback.Done() + config.Run()
	if options.Now != nil {
		total += options.Now()
	}
	return total
}
