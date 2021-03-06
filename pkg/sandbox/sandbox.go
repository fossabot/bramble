// Ever been to a playground? It's pretty easy to step in and out of a sandbox.
package sandbox

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"syscall"

	"github.com/maxmcd/bramble/pkg/logger"
	"github.com/maxmcd/bramble/pkg/store"
	"github.com/maxmcd/gosh/shell"
	"github.com/pkg/errors"
	"golang.org/x/sys/unix"
)

const (
	newNamespaceStepArg = "newNamespace"
	setupStepArg        = "setup"
	execStepArg         = "exec"
	setUIDExecName      = "bramble-setuid"
)

func firstArgMatchesStep() bool {
	switch os.Args[0] {
	case newNamespaceStepArg, setupStepArg, execStepArg:
		return true
	}
	return false
}

// Entrypoint must be run at the beginning of your executable. When the sandbox
// runs it re-runs the same binary with various arguments to indicate that we
// want the process to be run as a sandbox. If this function detects that it
// is needed it will run what it needs and then os.Exit the process, otherwise
// it will be a no-op.
func Entrypoint() {
	if !firstArgMatchesStep() {
		return
	}
	if err := entrypoint(); err != nil {
		fmt.Println(err.Error())
		os.Exit(1)
	}
	os.Exit(0)
}

func entrypoint() (err error) {
	if len(os.Args) <= 1 {
		return errors.New("unexpected argument count for sandbox step")
	}
	s, err := parseSerializedArg(os.Args[1])
	if err != nil {
		return err
	}
	switch os.Args[0] {
	case newNamespaceStepArg:
		return s.newNamespaceStep()
	case setupStepArg:
		return s.setupStep()
	case execStepArg:
		s.runExecStep()
		return nil
	default:
		return errors.New("first argument didn't match any known sandbox steps")
	}
}

// DebugFunction is a sandbox function that launches a rudimentary shell
var DebugFunction = RegisterFunction(func() {
	shell.Run()
})

// RunDebug launches a rudimentary shell within a sandbox
func RunDebug() (err error) {
	store, err := store.NewStore()
	if err != nil {
		return err
	}
	chrootPath, err := store.TempBuildDir()
	if err != nil {
		return err
	}
	s := &Sandbox{
		ChrootPath: chrootPath,
		Function:   DebugFunction,
		Stdin:      os.Stdin,
		Stderr:     os.Stderr,
		Stdout:     os.Stdout,
		Mounts:     []string{store.StorePath + ":ro"},
	}
	return s.Run(context.Background())
}

type Function struct {
	index int
}

func (f Function) MarshalJSON() ([]byte, error) {
	return json.Marshal(struct {
		Index int `json:"index"`
	}{
		Index: f.index,
	})
}
func (f *Function) UnmarshalJSON(data []byte) error {
	aux := struct {
		Index int `json:"index"`
	}{}
	err := json.Unmarshal(data, &aux)
	f.index = aux.Index
	return err
}

var registeredFunctions = []func(){}

// RegisterFunction can be used to register a Go function that you want to run
// in a sandbox instead of an external command. RegisterFunction must be
// called at the very beginning of your executable.
func RegisterFunction(fn func()) Function {
	index := len(registeredFunctions) + 1 // zero is null
	registeredFunctions = append(registeredFunctions, fn)
	return Function{index: index}
}

// Sandbox defines a command or function that you want to run in a sandbox
type Sandbox struct {
	Stdin      io.Reader `json:"-"`
	Stdout     io.Writer `json:"-"`
	Stderr     io.Writer `json:"-"`
	ChrootPath string
	Path       string
	Args       []string
	Dir        string
	Env        []string

	UserID  int
	GroupID int

	// Function can reference a function that has been created with
	// RegisterFunction. TODO: fix overloading of function and Path
	// functionality
	Function Function
	// Bind mounts or directories the process should have access too. These
	// should be absolute paths. If a mount is intended to be readonly add
	// ":ro" to the end of the path like `/tmp:ro`
	Mounts []string
	// DisableNetwork will remove network access within the sandbox process
	DisableNetwork bool
	// SetUIDBinary can be used if you want the parent process to call out
	// first to a different binary
	SetUIDBinary string // TODO
}

func (s Sandbox) serializeArg() (string, error) {
	byt, err := json.Marshal(s)
	return string(byt), err
}

func parseSerializedArg(arg string) (s Sandbox, err error) {
	return s, json.Unmarshal([]byte(arg), &s)
}

// Run runs the sandbox until execution has been completed
func (s Sandbox) Run(ctx context.Context) (err error) {
	serialized, err := s.serializeArg()
	if err != nil {
		return err
	}
	// TODO: allow reference to self
	path, err := exec.LookPath(setUIDExecName)
	if err != nil {
		return err
	}
	logger.Debugw("newSanbox", "execpath", path)
	// interrupt will be caught be the child process and the process
	// will exiting, causing this process to exit
	ignoreInterrupt()
	cmd := &exec.Cmd{
		Path:   path,
		Args:   []string{newNamespaceStepArg, serialized},
		Stdin:  s.Stdin,
		Stdout: s.Stdout,
		Stderr: s.Stderr,
	}
	errChan := make(chan error)
	go func() {
		if err := cmd.Run(); err != nil {
			errChan <- fmt.Errorf("error running newSandbox - %w", err)
		}
		close(errChan)
	}()
	select {
	case <-ctx.Done():
		if cmd.Process != nil {
			if err := cmd.Process.Signal(os.Interrupt); err != nil {
				return err
			}
		}
		// TODO: do this for all of them? Stop ignoring the interrupt in the children?
	case err = <-errChan:
		if err == nil && cmd.ProcessState != nil && cmd.ProcessState.ExitCode() != 0 {
			return errors.New("ah!a")
		}
		return err
	}
	return nil
}

func (s Sandbox) newNamespaceStep() (err error) {
	selfExe, err := os.Readlink("/proc/self/exe")
	if err != nil {
		return err
	}
	defer func() {
		logger.Debugw("clean up chrootDir", "path", s.ChrootPath)
		if er := os.RemoveAll(s.ChrootPath); er != nil {
			logger.Debugw("error cleaning up", "err", er)
			if err == nil {
				err = errors.Wrap(er, "error removing all files in "+s.ChrootPath)
			}
		}
	}()
	serialized, err := s.serializeArg()
	if err != nil {
		return err
	}

	var cloneFlags uintptr = syscall.CLONE_NEWUTS |
		syscall.CLONE_NEWNS |
		syscall.CLONE_NEWPID

	if s.DisableNetwork {
		cloneFlags |= syscall.CLONE_NEWNET
	}

	// interrupt will be caught be the child process and the process
	// will exiting, causing this process to exit
	ignoreInterrupt()
	cmd := &exec.Cmd{
		Path:   selfExe,
		Args:   []string{setupStepArg, serialized},
		Stdin:  os.Stdin,
		Stdout: os.Stdout,
		Stderr: os.Stderr,
		SysProcAttr: &syscall.SysProcAttr{
			Pdeathsig:  unix.SIGTERM, // ???
			Cloneflags: cloneFlags,
		},
	}
	return errors.Wrap(cmd.Run(), "error running newNamespace")
}

func (s Sandbox) setupStep() (err error) {
	logger.Debugw("setup chroot", "dir", s.ChrootPath)
	creds := &syscall.Credential{
		Gid: uint32(s.GroupID),
		Uid: uint32(s.UserID),
	}
	if err := os.Chown(s.ChrootPath, int(creds.Uid), int(creds.Gid)); err != nil {
		return err
	}

	chr := newChroot(s.ChrootPath, s.Mounts)
	defer func() {
		if er := chr.Cleanup(); er != nil {
			if err == nil {
				err = er
			} else {
				logger.Debugw("error during cleanup", "err", er)
			}
		}
	}()
	var selfExe string
	{
		// hardlink in executable
		selfExe, err = os.Readlink("/proc/self/exe")
		if err != nil {
			return err
		}
		if err := os.MkdirAll(filepath.Join(s.ChrootPath, filepath.Dir(selfExe)), 0777); err != nil {
			return err
		}
		if err = os.Link(selfExe, filepath.Join(s.ChrootPath, selfExe)); err != nil {
			return err
		}
	}

	if err := chr.Init(); err != nil {
		return err
	}

	serialized, err := s.serializeArg()
	if err != nil {
		return err
	}

	cmd := exec.CommandContext(interruptContext(), selfExe)
	cmd.Path = selfExe
	cmd.Args = []string{execStepArg, serialized}
	cmd.Env = append([]string{"USER=bramblebuild0", "HOME=/homeless"}, s.Env...)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.SysProcAttr = &syscall.SysProcAttr{
		Credential: creds,
	}
	return cmd.Run()
}

func ignoreInterrupt() {
	c := make(chan os.Signal)
	signal.Notify(c, os.Interrupt, syscall.SIGTERM)
	go func() {
		for {
			<-c
		}
	}()
}

func interruptContext() context.Context {
	c := make(chan os.Signal)
	signal.Notify(c, os.Interrupt, syscall.SIGTERM)
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		<-c
		cancel()
	}()
	return ctx
}

func (s Sandbox) runExecStep() {
	if s.Function.index != 0 {
		// TODO: env
		// TODO: dir
		registeredFunctions[s.Function.index-1]()
	} else {
		fmt.Println(s.Path, s.Args, s.Dir)
		cmd := exec.Cmd{
			Path: s.Path,
			Dir:  s.Dir,
			Args: append([]string{s.Path}, s.Args...),
			Env:  os.Environ(),

			// We don't use the passed sandbox stdio because
			// it's been passed to the very first run command
			Stdin:  os.Stdin,
			Stdout: os.Stdout,
			Stderr: os.Stderr,
		}
		if err := cmd.Run(); err != nil {
			fmt.Println(err.Error())
			os.Exit(1)
		}
	}
}
