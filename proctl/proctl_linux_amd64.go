// Package proctl provides functions for attaching to and manipulating
// a process during the debug session.
package proctl

import (
	"debug/elf"
	"debug/gosym"
	"fmt"
	"os"
	"syscall"
)

// Struct representing a debugged process. Holds onto pid, register values,
// process struct and process state.
type DebuggedProcess struct {
	Pid          int
	Regs         *syscall.PtraceRegs
	Process      *os.Process
	ProcessState *os.ProcessState
	Executable   *elf.File
	Symbols      []elf.Symbol
	GoSymTable   *gosym.Table
	BreakPoints  map[string]*BreakPoint
}

type BreakPoint struct {
	FunctionName string
	Line         int
	Addr         uint64
}

// Returns a new DebuggedProcess struct with sensible defaults.
func NewDebugProcess(pid int) (*DebuggedProcess, error) {
	proc, err := os.FindProcess(pid)
	if err != nil {
		return nil, err
	}

	err = syscall.PtraceAttach(pid)
	if err != nil {
		return nil, err
	}

	ps, err := proc.Wait()
	if err != nil {
		return nil, err
	}

	debuggedProc := DebuggedProcess{
		Pid:          pid,
		Regs:         &syscall.PtraceRegs{},
		Process:      proc,
		ProcessState: ps,
		BreakPoints:  make(map[string]*BreakPoint),
	}

	err = debuggedProc.LoadInformation()
	if err != nil {
		return nil, err
	}

	return &debuggedProc, nil
}

func (dbp *DebuggedProcess) LoadInformation() error {
	err := dbp.findExecutable()
	if err != nil {
		return err
	}

	err = dbp.obtainGoSymbols()
	if err != nil {
		return err
	}

	return nil
}

// Obtains register values from the debugged process.
func (dbp *DebuggedProcess) Registers() (*syscall.PtraceRegs, error) {
	err := syscall.PtraceGetRegs(dbp.Pid, dbp.Regs)
	if err != nil {
		return nil, fmt.Errorf("Registers():", err)
	}

	return dbp.Regs, nil
}

// Sets a breakpoint in the running process.
func (dbp *DebuggedProcess) Break(fname string) (*BreakPoint, error) {
	var (
		int3 = []byte{'0', 'x', 'C', 'C'}
		fn   = dbp.GoSymTable.LookupFunc(fname)
	)

	if fn == nil {
		return nil, fmt.Errorf("No function named %s\n", fname)
	}

	_, ok := dbp.BreakPoints[fname]
	if ok {
		return nil, fmt.Errorf("Breakpoint already set")
	}

	addr := uintptr(fn.LineTable.PC)
	_, err := syscall.PtracePokeData(dbp.Pid, addr, int3)
	if err != nil {
		return nil, err
	}

	breakpoint := &BreakPoint{
		FunctionName: fn.Name,
		Line:         fn.LineTable.Line,
		Addr:         fn.LineTable.PC,
	}

	dbp.BreakPoints[fname] = breakpoint

	return breakpoint, nil
}

// Steps through process.
func (dbp *DebuggedProcess) Step() error {
	err := dbp.handleResult(syscall.PtraceSingleStep(dbp.Pid))
	if err != nil {
		return fmt.Errorf("step failed: ", err.Error())
	}

	regs, err := dbp.Registers()
	if err != nil {
		return err
	}

	f, l, fn := dbp.GoSymTable.PCToLine(regs.PC())
	fmt.Printf("Stopped at: %s %s:%d\n", fn.Name, f, l)

	return nil
}

// Continue process until next breakpoint.
func (dbp *DebuggedProcess) Continue() error {
	return dbp.handleResult(syscall.PtraceCont(dbp.Pid, 0))
}

func (dbp *DebuggedProcess) handleResult(err error) error {
	if err != nil {
		return err
	}

	ps, err := dbp.Process.Wait()
	if err != nil {
		return err
	}

	dbp.ProcessState = ps

	return nil
}

func (dbp *DebuggedProcess) findExecutable() error {
	procpath := fmt.Sprintf("/proc/%d/exe", dbp.Pid)

	f, err := os.Open(procpath)
	if err != nil {
		return err
	}

	elffile, err := elf.NewFile(f)
	if err != nil {
		return err
	}

	dbp.Executable = elffile

	return nil
}

func (dbp *DebuggedProcess) obtainGoSymbols() error {
	symdat, err := dbp.Executable.Section(".gosymtab").Data()
	if err != nil {
		return err
	}

	pclndat, err := dbp.Executable.Section(".gopclntab").Data()
	if err != nil {
		return err
	}

	pcln := gosym.NewLineTable(pclndat, dbp.Executable.Section(".text").Addr)
	tab, err := gosym.NewTable(symdat, pcln)
	if err != nil {
		return err
	}

	dbp.GoSymTable = tab

	return nil
}
