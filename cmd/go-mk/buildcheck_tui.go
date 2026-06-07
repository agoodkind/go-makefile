// Bubble Tea progress display for a check run on a terminal. The model renders
// the same report layout the streamed and batch paths produce, with a spinner on
// the running step, and updates each row to its result as the run reports it. The
// first frame paints immediately with every step listed and the first one
// spinning, so the run gives feedback the moment it launches. The final frame is
// the full report (rows, findings, footer), so what stays on screen matches
// report.Render.
package main

import (
	"log/slog"
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/bubbles/v2/spinner"

	"goodkind.io/go-makefile/internal/report"
)

// stepView is one row's live state: its name, whether it has resolved, and its
// resolved result.
type stepView struct {
	name   string
	done   bool
	result report.StepResult
}

// gateDoneMsg reports that the check at index resolved to result.
type gateDoneMsg struct {
	index  int
	result report.StepResult
}

// runFinishedMsg reports that every check finished, carrying the aggregate exit
// status.
type runFinishedMsg int

// checkModel is the Bubble Tea model for a check run.
type checkModel struct {
	title    string
	width    int
	steps    []stepView
	spinner  spinner.Model
	finished bool
	status   int
}

// newCheckModel builds the model with every step pending and a spinner ready on
// the first step.
func newCheckModel(title string, width int, checks []check) *checkModel {
	steps := make([]stepView, len(checks))
	for index, current := range checks {
		steps[index] = stepView{name: current.name}
	}
	spin := spinner.New()
	spin.Spinner = spinner.MiniDot
	return &checkModel{title: title, width: width, steps: steps, spinner: spin}
}

// Init starts the spinner animation.
func (m *checkModel) Init() tea.Cmd {
	return m.spinner.Tick
}

// Update advances the spinner, records each resolved step, and quits once the
// run reports it finished or the user interrupts.
func (m *checkModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch message := msg.(type) {
	case gateDoneMsg:
		if message.index >= 0 && message.index < len(m.steps) {
			m.steps[message.index].done = true
			m.steps[message.index].result = message.result
		}
		return m, nil
	case runFinishedMsg:
		m.finished = true
		m.status = int(message)
		return m, tea.Quit
	case spinner.TickMsg:
		var cmd tea.Cmd
		m.spinner, cmd = m.spinner.Update(message)
		return m, cmd
	default:
		return m, nil
	}
}

// View renders the current frame. While the run is in progress it shows the
// title and one status row per step, with a spinner on the running step. Once
// finished it renders the full report through report.Render, so the frame that
// stays on screen is byte-identical to the batch and streamed output.
func (m *checkModel) View() tea.View {
	if m.finished {
		content := report.Render(report.Report{Title: m.title, Steps: m.resolvedSteps()})
		return tea.NewView(strings.TrimRight(content, "\n"))
	}

	var builder strings.Builder
	builder.WriteString(m.title)
	builder.WriteString("\n\n")
	running := m.firstPendingIndex()
	for index, step := range m.steps {
		builder.WriteString(strings.TrimRight(m.row(index, step, running), " "))
		builder.WriteString("\n")
	}
	return tea.NewView(strings.TrimRight(builder.String(), "\n"))
}

// resolvedSteps returns the resolved StepResult for every step, naming any step
// that has not resolved so a partial render still lists it.
func (m *checkModel) resolvedSteps() []report.StepResult {
	steps := make([]report.StepResult, len(m.steps))
	for index, step := range m.steps {
		if step.done {
			steps[index] = step.result
			continue
		}
		steps[index] = report.StepResult{Name: step.name}
	}
	return steps
}

// row renders one status row: the resolved label for a done step, the spinner
// for the running step, or a blank cell for a pending step.
func (m *checkModel) row(index int, step stepView, running int) string {
	if step.done {
		return report.StepRow(m.width, step.result)
	}
	if index == running {
		return report.Row(m.width, step.name, m.spinner.View())
	}
	return report.Row(m.width, step.name, "")
}

// firstPendingIndex returns the index of the first not-yet-done step, or -1 when
// all steps are done.
func (m *checkModel) firstPendingIndex() int {
	for index, step := range m.steps {
		if !step.done {
			return index
		}
	}
	return -1
}

// runChecksTUI runs the checks under a Bubble Tea progress display and returns
// the aggregate exit status. The checks run exactly once in a goroutine that
// feeds the model each result and records it for the fallback. When Bubble Tea
// cannot drive the terminal (no usable TTY), the run still completes and the
// report is printed through report.Render, so the output is never lost and the
// checks are never run twice.
func runChecksTUI(title string, checks []check) int {
	width := checkNameWidth(checks)
	model := newCheckModel(title, width, checks)
	// A progress display needs no keyboard input. Disabling input keeps the run
	// from opening the controlling TTY for input and from quitting early on a
	// stdin EOF. SIGINT still terminates the process.
	program := tea.NewProgram(model, tea.WithInput(nil))
	results := make([]report.StepResult, len(checks))
	done := make(chan int, 1)
	go func() {
		status := 1
		defer func() {
			if r := recover(); r != nil {
				slog.Error("build-check progress runner panicked", slog.Any("err", r))
			}
			safeSend(program, runFinishedMsg(status))
			done <- status
		}()
		status = executeChecks(checks, func(index int, result report.StepResult) {
			results[index] = result
			safeSend(program, gateDoneMsg{index: index, result: result})
		})
	}()
	_, err := program.Run()
	status := <-done
	if err != nil {
		writeStdout(report.Render(report.Report{Title: title, Steps: results}))
	}
	return status
}

// safeSend delivers a message to the program, recovering if the program has
// already stopped so the check runner finishes even when Bubble Tea ended early.
func safeSend(program *tea.Program, msg tea.Msg) {
	defer func() { _ = recover() }()
	program.Send(msg)
}
