package plugins

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"sync"
	"time"
)

// effectDone ends a run of a service. It is never returned as an effect.
const effectDone = "done"

// service is one long-running process that serves the runs of every plugin
// naming it. A request goes to its standard input as one JSON line with an
// "id"; each effect line it prints names the run with the same "id", and
// "done" ends that run. Its runs don't wait for each other.
type service struct {
	name  string
	onRun []string

	mutex   sync.Mutex
	process *serviceProcess // nil until started, and again once it ended
	next    int             // the last run identifier handed out
}

// serviceProcess is one start of a service.
type serviceProcess struct {
	input   *os.File
	writing sync.Mutex // one request line at a time
	// pending are the runs waiting for "done", by identifier. Guarded by the
	// service's mutex.
	pending map[string]*pendingRun
}

// pendingRun gathers the effects of one run until "done".
type pendingRun struct {
	effects []Effect
	size    int               // bytes of effect lines so far
	reply   chan serviceReply // buffered: written once, by whoever ends the run
}

type serviceReply struct {
	effects []Effect
	err     error
}

// ask sends the request to the plugin's service, starting it if it isn't
// running, and waits for the run's "done". The timeout starts here, with the
// run. A run that ends before "done" (timeout, cancel) leaves the service
// running; whatever it prints for the run later is dropped.
func (runner *Runner) ask(ctx context.Context, found *entry, request Request) ([]Effect, error) {
	name := found.plugin.Command
	runContext, cancel := runner.withTimeout(ctx, found.plugin.Timeout)
	defer cancel()

	process, identifier, reply, err := runner.begin(found.service)
	if err != nil {
		return nil, fmt.Errorf("start plugin %q: %w", name, err)
	}
	defer found.service.forget(process, identifier)
	line, err := json.Marshal(struct {
		Identifier string `json:"id"`
		Request
	}{identifier, request})
	if err != nil {
		return nil, fmt.Errorf("encode request for plugin %q: %w", name, err)
	}
	// The write fails if the service ended; the reply then says why.
	if err := process.write(runContext, append(line, '\n')); err == nil {
		runner.sent(name)
	}
	select {
	case answered := <-reply:
		if answered.err != nil {
			return nil, fmt.Errorf("plugin %q: %w", name, answered.err)
		}
		return answered.effects, nil
	case <-runContext.Done():
	}
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	return nil, fmt.Errorf("plugin %q: %w after %s", name, ErrTimeout, found.plugin.Timeout)
}

// begin hands out a run of the service, starting the process if needed.
func (runner *Runner) begin(serving *service) (*serviceProcess, string, <-chan serviceReply, error) {
	serving.mutex.Lock()
	defer serving.mutex.Unlock()
	if serving.process == nil {
		process, err := runner.startService(serving)
		if err != nil {
			return nil, "", nil, err
		}
		serving.process = process
	}
	serving.next++
	identifier := strconv.Itoa(serving.next)
	waiting := &pendingRun{reply: make(chan serviceReply, 1)}
	serving.process.pending[identifier] = waiting
	return serving.process, identifier, waiting.reply, nil
}

// forget drops a run that no longer waits, so later lines for it are dropped.
func (serving *service) forget(process *serviceProcess, identifier string) {
	serving.mutex.Lock()
	defer serving.mutex.Unlock()
	delete(process.pending, identifier)
}

// write sends one request line. A service that doesn't read its input blocks
// the write only until the run's deadline.
func (process *serviceProcess) write(ctx context.Context, line []byte) error {
	process.writing.Lock()
	defer process.writing.Unlock()
	deadline, _ := ctx.Deadline() // the zero time is no deadline
	if err := process.input.SetWriteDeadline(deadline); err != nil {
		return err
	}
	_, err := process.input.Write(line)
	return err
}

// startService starts the process. It is called with the service's mutex
// held. The process lives until it exits or Stop kills it, in a session of
// its own like a run's.
func (runner *Runner) startService(serving *service) (*serviceProcess, error) {
	program, err := runner.program(serving.onRun[0])
	if err != nil {
		return nil, err
	}
	// Counted before it starts, so Stop waits for it; none starts after Stop.
	runner.stopping.Lock()
	if runner.stopped {
		runner.stopping.Unlock()
		return nil, errors.New("strig is exiting")
	}
	runner.serviceProcesses.Add(1)
	runner.stopping.Unlock()
	started := false
	defer func() {
		if !started {
			runner.serviceProcesses.Done()
		}
	}()
	inputReader, inputWriter, err := os.Pipe()
	if err != nil {
		return nil, err
	}
	outputReader, outputWriter, err := os.Pipe()
	if err != nil {
		return nil, errors.Join(err, inputReader.Close(), inputWriter.Close())
	}
	command := exec.CommandContext(runner.serviceContext, program, serving.onRun[1:]...)
	command.Env = runner.environment
	command.Stdin, command.Stdout = inputReader, outputWriter
	standardError, flushLog := runner.standardErrorSink(serving.name)
	command.Stderr = &boundedLog{writer: standardError, remaining: MaximumErrorOutputSize, cutOff: func() {
		runner.logger.Warn("plugin standard error cut off", "plugin", serving.name, "limit", MaximumErrorOutputSize)
	}}
	command.WaitDelay = runner.waitDelay
	detach(command)
	startError := command.Start()
	// The child has its own copies; strig keeps only its ends.
	closeError := errors.Join(inputReader.Close(), outputWriter.Close())
	if startError != nil {
		return nil, errors.Join(startError, inputWriter.Close(), outputReader.Close())
	}
	if closeError != nil {
		runner.logger.Warn("close the service's pipe ends", "plugin", serving.name, "err", closeError)
	}
	process := &serviceProcess{input: inputWriter, pending: map[string]*pendingRun{}}
	read := make(chan error, 1)
	started = true
	go func() { read <- runner.readService(serving, process, outputReader, command) }()
	go runner.waitService(serving, process, command, outputReader, read, flushLog)
	return process, nil
}

// readService reads the effect lines until the output ends. A line past the
// output limit can't be read, so it kills the process.
func (runner *Runner) readService(serving *service, process *serviceProcess, output *os.File, command *exec.Cmd) error {
	scanner := bufio.NewScanner(output)
	scanner.Buffer(make([]byte, 0, 64<<10), MaximumOutputSize)
	for scanner.Scan() {
		runner.serviceLine(serving, process, scanner.Bytes())
	}
	err := scanner.Err()
	if errors.Is(err, bufio.ErrTooLong) {
		runner.logger.Warn("plugin service printed a line over the limit; killed", "plugin", serving.name, "limit", MaximumOutputSize)
		_ = command.Process.Kill() // it may have exited already
		return ErrOutputTooLarge
	}
	return err
}

// serviceLine handles one line. A line naming no run under way is logged and
// dropped, so a service survives bad output; a bad effect of a run fails that
// run, as it fails a one-off run.
func (runner *Runner) serviceLine(serving *service, process *serviceProcess, line []byte) {
	line = bytes.TrimSpace(line)
	if len(line) == 0 {
		return
	}
	var header struct {
		Identifier *string `json:"id"`
		Name       string  `json:"name"`
	}
	if err := json.Unmarshal(line, &header); err != nil || header.Identifier == nil {
		runner.logger.Warn("plugin service printed a line that is not an effect of a run; skipped", "plugin", serving.name)
		return
	}
	serving.mutex.Lock()
	defer serving.mutex.Unlock()
	identifier := *header.Identifier
	waiting, ok := process.pending[identifier]
	if !ok {
		runner.logger.Info("plugin service printed an effect for no run under way; dropped", "plugin", serving.name, "effect", header.Name)
		return
	}
	end := func(reply serviceReply) {
		delete(process.pending, identifier)
		waiting.reply <- reply
	}
	if header.Name == effectDone {
		end(serviceReply{effects: waiting.effects})
		return
	}
	waiting.size += len(line)
	if waiting.size > MaximumOutputSize {
		end(serviceReply{err: fmt.Errorf("%w (limit %d bytes)", ErrOutputTooLarge, MaximumOutputSize)})
		return
	}
	effect, known, err := decodeEffect(line)
	switch {
	case err != nil:
		end(serviceReply{err: ErrInvalidOutput})
	case !known:
		runner.skippedEffect(serving.name, effect.Name)
	default:
		waiting.effects = append(waiting.effects, effect)
	}
}

// waitService waits for the process to end, then fails its runs still under
// way, so the next run starts it again.
func (runner *Runner) waitService(serving *service, process *serviceProcess, command *exec.Cmd, output *os.File, read <-chan error, flushLog func()) {
	defer runner.serviceProcesses.Done()
	waitError := command.Wait()
	// Something the service started can hold its output open; reading stops
	// a moment after the service itself ended.
	var readError error
	select {
	case readError = <-read:
	case <-time.After(runner.waitDelay):
		_ = output.SetReadDeadline(time.Now())
		readError = <-read
	}
	flushLog()
	runner.logger.Info("plugin service ended", "plugin", serving.name, "err", waitError)
	reason := ErrExited
	if errors.Is(readError, ErrOutputTooLarge) {
		reason = fmt.Errorf("%w (limit %d bytes)", ErrOutputTooLarge, MaximumOutputSize)
	}

	serving.mutex.Lock()
	defer serving.mutex.Unlock()
	for identifier, waiting := range process.pending {
		delete(process.pending, identifier)
		waiting.reply <- serviceReply{err: reason}
	}
	if serving.process == process {
		serving.process = nil
	}
	if err := errors.Join(output.Close(), process.input.Close()); err != nil {
		runner.logger.Warn("close the service's pipes", "plugin", serving.name, "err", err)
	}
}

// Stop kills the services' processes and returns once they have ended; runs
// waiting on one fail. cmd/strig calls it as strig exits, after the runs were
// cancelled. A run after Stop can't start a service.
func (runner *Runner) Stop() {
	runner.stopping.Lock()
	runner.stopped = true
	runner.stopping.Unlock()
	runner.stopServices()
	runner.serviceProcesses.Wait()
}
