package chat

import (
	"context"

	"github.com/findy-network/findy-agent-api/grpc/agency"
	"github.com/findy-network/findy-common-go/agency/client"
	"github.com/findy-network/findy-common-go/agency/client/async"
	"github.com/findy-network/findy-common-go/agency/fsm"
	"github.com/golang/glog"
	"github.com/lainio/err2"
)

type ConnStatus *agency.AgentStatus

type StatusChan chan ConnStatus

type Conversation struct {
	StatusChan

	id string
	client.Conn
	lastProtocolID map[string]struct{} //*agency.ProtocolID

	// machine can be ptr because multiplexer creates a new for each one
	machine *fsm.Machine
}

// These are class level variables for this chat bot which means that every
// conversation of this bot will share these variables
var (
	// Status is input channel for multiplexing this chat bot i.e. CA sends
	// every message t this channel from here they are transported to according
	// conversations
	Status = make(StatusChan)

	// conversations is a map of all instances indexed by connection id aka
	// pairwise id
	conversations = make(map[string]*Conversation)

	// Machine is the initial finite-state machine from where every
	// conversations will be started
	Machine fsm.MachineData
)

func Multiplexer(conn client.Conn) {
	glog.V(4).Infoln("starting multiplexer", Machine.FType)
	for {
		t := <-Status
		c, ok := conversations[t.Notification.ConnectionId]
		if !ok {
			glog.V(5).Infoln("Starting new conversation",
				Machine.FType)
			c = &Conversation{
				id:         t.Notification.ConnectionId,
				Conn:       conn,
				StatusChan: make(StatusChan),
			}
			go c.RunConversation(Machine)
			conversations[t.Notification.ConnectionId] = c
		}
		c.StatusChan <- t
	}
}

func (c *Conversation) RunConversation(data fsm.MachineData) {
	c.machine = fsm.NewMachine(data)
	err2.Check(c.machine.Initialize())

	c.send(c.machine.Start(), nil)

	for {
		t := <-c.StatusChan

		glog.V(10).Infoln("conversation:", t.Notification.ConnectionId)

		switch t.Notification.TypeId {
		case agency.Notification_STATUS_UPDATE:
			glog.V(3).Infoln("status update")
			if c.IsOursAndRm(t.Notification.ProtocolId) {
				glog.V(10).Infoln("discarding event")
				continue
			}

			status := c.getStatus(t)

			if transition := c.machine.Triggers(status); transition != nil {

				// todo: different transitions to FSM, move error handling to it!
				if status.GetState().State != agency.ProtocolState_OK {
					glog.Warningln("current FSM steps only completed protocol steps", status.GetState().State)
					continue
				}
				if glog.V(3) {
					glog.Infof("machine: %s (%p)", c.machine.Name, c.machine)
					glog.Infoln("role:", status.GetState().ProtocolId.Role)
					glog.Infoln("TRiGGERiNG", transition.Trigger.ProtocolType)
				}

				c.send(transition.BuildSendEvents(status), t)

				c.machine.Step(transition)
			} else {
				glog.V(1).Infoln("machine don't have transition for:",
					t.Notification.ProtocolType)
			}
		case agency.Notification_ANSWER_NEEDED_PROOF_VERIFY:
			glog.V(1).Infof("- %s: proof QA (%p)", c.machine.Name,
				c.machine)
			if transition := c.machine.Answers(t); transition != nil {
				c.send(transition.BuildSendAnswers(t), t)
				c.machine.Step(transition)
			}
		}

	}
}

func (c *Conversation) getStatus(status ConnStatus) *agency.ProtocolStatus {
	ctx := context.Background()
	didComm := agency.NewDIDCommClient(c.Conn)
	statusResult, err := didComm.Status(ctx, &agency.ProtocolID{
		TypeId:           status.Notification.ProtocolType,
		Id:               status.Notification.ProtocolId,
		NotificationTime: status.Notification.Timestamp,
	})
	err2.Check(err)
	return statusResult
}

func (c *Conversation) reply(status *agency.AgentStatus, ack bool) {
	ctx := context.Background()
	agentClient := agency.NewAgentClient(c.Conn)
	cid, err := agentClient.Give(ctx, &agency.Answer{
		Id:       status.Notification.Id,
		ClientId: status.ClientId,
		Ack:      ack,
		Info:     "testing says hello!",
	})
	err2.Check(err)
	glog.V(3).Infof("Sending the answer (%s) send to client:%s\n",
		status.Notification.Id, cid.Id)
}

func (c *Conversation) sendBasicMessage(message *fsm.BasicMessage, noAck bool) {
	r, err := async.NewPairwise(
		c.Conn,
		c.id,
	).BasicMessage(context.Background(),
		message.Content)
	err2.Check(err)
	glog.V(10).Infoln("protocol id:", r.Id)
	if noAck {
		c.SetLastProtocolID(r)
	}
}

func (c *Conversation) sendIssuing(message *fsm.Issuing, noAck bool) {
	r, err := async.NewPairwise(
		c.Conn,
		c.id,
	).Issue(context.Background(),
		message.CredDefID, message.AttrsJSON)
	err2.Check(err)
	glog.V(10).Infoln("protocol id:", r.Id)
	if noAck {
		c.SetLastProtocolID(r)
	}
}

func (c *Conversation) sendReqProof(message *fsm.Proof, noAck bool) {
	r, err := async.NewPairwise(
		c.Conn,
		c.id,
	).ReqProof(context.Background(),
		message.ProofJSON)
	err2.Check(err)
	glog.V(10).Infoln("protocol id:", r.Id)
	if noAck {
		c.SetLastProtocolID(r)
	}
}

func (c *Conversation) sendEmail(message *fsm.Email, _ bool) {
	// todo: implement send email here
	glog.V(0).Infoln("sending email to", message.To, message.Body)
}

func (c *Conversation) SetLastProtocolID(pid *agency.ProtocolID) {
	if c.lastProtocolID == nil {
		c.lastProtocolID = make(map[string]struct{})
	}
	c.lastProtocolID[pid.Id] = struct{}{}
}

func (c *Conversation) send(outputs []*fsm.Event, status ConnStatus) {
	if outputs == nil {
		return
	}
	for _, output := range outputs {
		switch output.ProtocolType {
		case agency.Protocol_CONNECT:
			glog.Warningf("we should not be here!!")
		case agency.Protocol_BASIC_MESSAGE:
			c.sendBasicMessage(output.BasicMessage, output.NoStatus)
		case agency.Protocol_ISSUE:
			c.sendIssuing(output.Issuing, output.NoStatus)
		case agency.Protocol_PROOF:
			c.sendReqProof(output.Proof, output.NoStatus)
		case fsm.EmailProtocol:
			c.sendEmail(output.Email, output.NoStatus)
		case fsm.QAProtocol:
			if status == nil {
				panic("FSM syntax error")
			}
			ack := false
			if output.Data == "ACK" {
				ack = true
			}
			c.reply(status, ack)
		}
	}
}

func (c *Conversation) IsOursAndRm(id string) bool {
	if _, ok := c.lastProtocolID[id]; ok {
		c.lastProtocolID[id] = struct{}{}
		return true
	}
	return false
}
