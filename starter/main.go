package main

import (
	"context"
	"log"
	"time"

	"github.com/pborman/uuid"
	"go.temporal.io/sdk/client"

	"childcancellation"
)

func main() {
	// The client is a heavyweight object that should be created once per process.
	c, err := client.Dial(client.Options{
		HostPort: client.DefaultHostPort,
	})
	if err != nil {
		log.Fatalln("Unable to create client", err)
	}
	defer c.Close()

	workflowOptions := client.StartWorkflowOptions{
		ID:        "parent_" + uuid.New(),
		TaskQueue: childcancellation.TaskQueue,
	}

	we, err := c.ExecuteWorkflow(context.Background(), workflowOptions, childcancellation.Parent)
	if err != nil {
		log.Fatalln("Unable to execute workflow", err)
	}
	log.Println("Started workflow", "WorkflowID", we.GetID(), "RunID", we.GetRunID())

	time.Sleep(time.Second * 2)

	if err := c.CancelWorkflow(context.Background(), we.GetID(), we.GetRunID()); err != nil {
		log.Fatalln("Unable to cancel workflow", err)
	}

	// Synchronously wait for the workflow completion.
	//var result string
	//err = we.Get(context.Background(), &result)
	//if err != nil {
	//	log.Fatalln("Unable get workflow result", err)
	//}
	//log.Println("Workflow result:", result)
}
