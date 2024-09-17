package childcancellation

import (
	"context"
	"fmt"
	"github.com/pborman/uuid"
	"go.temporal.io/api/enums/v1"
	"go.temporal.io/sdk/client"
	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/workflow"
	"time"
)

const TaskQueue = "childcancellation"
const numChildren = 1

type Activities struct {
	TemporalClient client.Client
}

func (a *Activities) Echo(event Event) (string, error) {
	return event.String(), nil
}

type SignalWorkflowInput struct {
	WorkflowID string
	RunID      string
	SignalName string
	SignalArg  interface{}
}

func (a *Activities) SignalWorkflow(ctx context.Context, in SignalWorkflowInput) error {
	err := a.TemporalClient.SignalWorkflow(
		ctx,
		in.WorkflowID,
		in.RunID,
		in.SignalName,
		in.SignalArg,
	)
	if err != nil {
		return fmt.Errorf("failed to signal workflow: %w", err)
	}
	return nil
}

func Parent(ctx workflow.Context) error {
	ao := workflow.ActivityOptions{
		StartToCloseTimeout: 10 * time.Second,
	}
	ctx = workflow.WithActivityOptions(ctx, ao)
	var err error
	numCompletedChildren := 0

	for i := 0; i < numChildren; i++ {
		workflow.Go(ctx, func(gCtx workflow.Context) {
			childOpts := workflow.ChildWorkflowOptions{
				WorkflowID:          "child_" + uuid.New(),
				TaskQueue:           TaskQueue,
				WaitForCancellation: true,
				ParentClosePolicy:   enums.PARENT_CLOSE_POLICY_REQUEST_CANCEL,
			}
			childCtx := workflow.WithChildOptions(gCtx, childOpts)

			childFuture := workflow.ExecuteChildWorkflow(childCtx, Child)

			if err := processChildEvents(gCtx, childFuture, childEventHandler); err != nil {
				err = fmt.Errorf("processing child events: %w", err)
			}

			if err := childFuture.Get(childCtx, nil); err != nil {
				err = fmt.Errorf("running child workflow: %w", err)
			}
			numCompletedChildren++
		})
	}

	_ = workflow.Await(ctx, func() bool {
		return err != nil || numCompletedChildren == numChildren
	})

	return nil
}

func childEventHandler(ctx workflow.Context, event Event) error {
	var a *Activities
	var result string
	if err := workflow.ExecuteActivity(ctx, a.Echo, event).Get(ctx, &result); err != nil {
		return fmt.Errorf("handling child event: %w", err)
	}
	logger := workflow.GetLogger(ctx)
	logger.Info("Received child event", "Event", result)
	return nil
}

func processChildEvents(
	ctx workflow.Context,
	childFuture workflow.ChildWorkflowFuture,
	eventHandler func(ctx workflow.Context, event Event) error,
) error {
	// Wait for child workflow to start and get execution info
	var childExecution workflow.Execution
	if err := childFuture.GetChildWorkflowExecution().Get(ctx, &childExecution); err != nil {
		return fmt.Errorf("starting child workflow: %w", err)
	}
	// Listen for events from child workflow
	signalChan := workflow.GetSignalChannel(ctx, childExecution.RunID)
	selector := workflow.NewNamedSelector(ctx, fmt.Sprintf("child-events-%s", childExecution.RunID))
	// This child context will be canceled if the parent context is cancelled, or when
	// we cancel it in the select loop below (as a result of the output channel closing).
	// This lets us unify our end condition to just "when the context is cancelled".
	ctx, cancel := workflow.WithCancel(ctx)
	defer cancel()

	// We register this no-op callback on the selector to short-circuit the loop
	// when the context is done.
	selector.AddReceive(ctx.Done(), func(_ workflow.ReceiveChannel, _ bool) {})

	var loopError error
	selector.AddReceive(signalChan, func(c workflow.ReceiveChannel, more bool) {
		if !more {
			cancel()
			return
		}

		var event Event
		if channelStillOpen := c.Receive(ctx, &event); !channelStillOpen {
			loopError = fmt.Errorf("unexpected signal channel closure for child events")
			cancel()
			return
		}

		if err := eventHandler(ctx, event); err != nil {
			loopError = fmt.Errorf("handling child events: %w", err)
			cancel()
			return
		}
		if event.IsFinal {
			cancel()
			return
		}
	})

	for ctx.Err() == nil {
		selector.Select(ctx)
	}

	if loopError != nil {
		return loopError
	}
	return nil
}

type Event struct {
	Value   string
	IsFinal bool
}

func (e Event) String() string {
	return fmt.Sprintf("Value: %s | IsFinal: %t", e.Value, e.IsFinal)
}

func Child(ctx workflow.Context) error {

	ao := workflow.ActivityOptions{
		StartToCloseTimeout: 10 * time.Second,
		RetryPolicy:         &temporal.RetryPolicy{MaximumAttempts: 1},
	}
	ctx = workflow.WithActivityOptions(ctx, ao)

	cleanupCtx, cleanupCancel := workflow.NewDisconnectedContext(ctx)
	defer cleanupCancel()

	wfInfo := workflow.GetInfo(ctx)
	var a *Activities

	err := func(ctx workflow.Context) error {
		beforeSleepInput := SignalWorkflowInput{
			WorkflowID: wfInfo.ParentWorkflowExecution.ID,
			RunID:      wfInfo.ParentWorkflowExecution.RunID,
			SignalName: wfInfo.WorkflowExecution.RunID,
			SignalArg:  Event{Value: "Before sleep"},
		}
		if err := workflow.ExecuteActivity(ctx, a.SignalWorkflow, beforeSleepInput).Get(ctx, nil); err != nil {
			return fmt.Errorf("signalling before sleep event: %w", err)
		}
		if err := workflow.Sleep(ctx, time.Second*10); err != nil {
			return fmt.Errorf("child sleeping: %w", err)
		}
		afterSleepInput := SignalWorkflowInput{
			WorkflowID: wfInfo.ParentWorkflowExecution.ID,
			RunID:      wfInfo.ParentWorkflowExecution.RunID,
			SignalName: wfInfo.WorkflowExecution.RunID,
			SignalArg:  Event{Value: "After sleep"},
		}
		if err := workflow.ExecuteActivity(ctx, a.SignalWorkflow, afterSleepInput).Get(ctx, nil); err != nil {
			return fmt.Errorf("signalling after sleep event: %w", err)
		}
		return nil
	}(ctx)

	// Send final update regardless of how things went.
	finalUpdateInput := SignalWorkflowInput{
		WorkflowID: wfInfo.ParentWorkflowExecution.ID,
		RunID:      wfInfo.ParentWorkflowExecution.RunID,
		SignalName: wfInfo.WorkflowExecution.RunID,
		SignalArg:  Event{Value: "Final update", IsFinal: true},
	}
	if finalUpdateErr := workflow.ExecuteActivity(cleanupCtx, a.SignalWorkflow, finalUpdateInput).Get(cleanupCtx, nil); finalUpdateErr != nil {
		return fmt.Errorf("signalling after sleep event: %w", finalUpdateErr)
	}

	return err
}
