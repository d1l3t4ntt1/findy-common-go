package fsm

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math"
	"math/rand"
	"text/template"

	agency "github.com/findy-network/findy-common-go/grpc/agency/v1"
	"github.com/golang/glog"
	"github.com/lainio/err2"
	"github.com/lainio/err2/assert"
	"github.com/lainio/err2/try"
)

type Transition struct {
	Trigger *Event `json:"trigger,omitempty"`

	Sends []*Event `json:"sends,omitempty"`

	Target string `json:"target"`

	// Script, or something to execute in future?? idea we could have LUA
	// script which communicates our Memory map, that would be a simple data
	// model

	Machine *Machine `json:"-"`
}

// BuildSendEventsFromBackendData is called from b-fsm and it's combined method
// to execute two phases. Build input events and then build send event list. The
// second phase is shared with pw-fsm.
// TODO: Note that the input RULE handling is minimal and many methods are
// missing!
func (t *Transition) BuildSendEventsFromBackendData(data *BackendData) []*Event {
	var (
		usedProtocol agency.Protocol_Type = BackendProtocol
		eData                             = &EventData{Backend: data}
	)
	glog.Infof("Data: '%v', machine: %v", t.Trigger.Data, t.Machine.Type)
	if t.Machine.Type == MachineTypeConversation {
		glog.V(2).Infoln("+++ conversation machines send Backend msgs as BM")
		usedProtocol = agency.Protocol_BASIC_MESSAGE
		eData = &EventData{BasicMessage: &BasicMessage{
			Content: data.Content,
		}}
		e := &Event{
			Protocol:     toFileProtocolType[usedProtocol],
			ProtocolType: usedProtocol,
			EventData:    eData,
			Data:         data.Content,
		}
		return t.doBuildSendEvents(e)
	} else if t.Machine.Type == MachineTypeBackend {
		e := &Event{
			Protocol:     toFileProtocolType[usedProtocol],
			ProtocolType: usedProtocol,
			EventData:    eData,
			Data:         data.Content,
		}
		glog.Infoln("RULE:", t.Trigger.Rule)
		switch t.Trigger.Rule {
		case TriggerTypeValidateInputNotEqual, TriggerTypeValidateInputEqual,
			TriggerTypeLua, TriggerTypeUseInput, TriggerTypeTransient:
			// for future use

		case TriggerTypeUseInputSaveSessionID:
			key := data.ConnID + LUA_SESSION_ID
			sessionID := data.Content
			t.Machine.Memory[key] = sessionID
			eData.Backend.SessionID = sessionID
			glog.V(3).Infoln("=== save to machine memory", key, "->", sessionID)
		case TriggerTypeUseInputSaveConnID:
			t.Machine.Memory[data.ConnID+t.Trigger.Data] = data.Content
			glog.V(3).Infoln("=== save to machine memory", data.ConnID+t.Trigger.Data, "->", data.Content)
		case TriggerTypeUseInputSave:
			t.Machine.Memory[t.Trigger.Data] = data.Content
			glog.V(3).Infoln("=== save to machine memory", t.Trigger.Data, "->", data.Content)

		case TriggerTypeData, TriggerTypeInputEqual:
			// for future use
		}
		return t.doBuildSendEvents(e)
	} else {
		assert.That(false, "unknown machine type")
	}
	return nil
}

func (t *Transition) BuildSendEventsFromStep(data string) []*Event {
	input := &Event{
		Protocol:     toFileProtocolType[TransientProtocol],
		ProtocolType: TransientProtocol,
		EventData:    &EventData{BasicMessage: &BasicMessage{Content: data}},
	}
	return t.doBuildSendEvents(input)
}

func (t *Transition) BuildSendEventsFromHook(hookData map[string]string) []*Event {
	input := &Event{
		Protocol:     toFileProtocolType[HookProtocol],
		ProtocolType: HookProtocol,
		EventData:    &EventData{Hook: &Hook{Data: hookData}},
	}
	return t.doBuildSendEvents(input)
}

func (t *Transition) BuildSendEvents(status *agency.ProtocolStatus) []*Event {
	input := t.buildInputEvent(status)
	return t.doBuildSendEvents(input)
}

func (t *Transition) doBuildSendEvents(input *Event) []*Event {
	events := t.Sends
	sends := make([]*Event, len(events))
	for i, send := range events {
		sends[i] = send
		switch send.Protocol {
		case MessageIssueCred:
			switch send.Rule {
			case TriggerTypeFormatFromMem:
				send.EventData = &EventData{Issuing: &Issuing{
					CredDefID: send.EventData.Issuing.CredDefID,
					AttrsJSON: t.FmtFromMem(send),
				}}
			}
		case MessagePresentProof:
			switch send.Rule {
			case TriggerTypeData:
				send.EventData = &EventData{Proof: &Proof{
					ProofJSON: send.Data,
				}}
			}
		case MessageAnswer:
			glog.V(3).Infoln("building answer") // it's so easy
		case MessageEmail:
			switch send.Rule {
			case TriggerTypePIN:
				t.GenPIN(send)
				emailJSON := t.FmtFromMem(send)
				var email Email
				err := json.Unmarshal([]byte(emailJSON), &email)
				if err != nil {
					glog.Errorf("json error %v", err)
				}
				glog.V(1).Infoln("email:", emailJSON)
				send.EventData = &EventData{Email: &email}
			}
		case MessageBasicMessage:
			t.buildBMSend(input, send)
		case MessageHook:
			t.buildHookSend(input, send)
		case MessageBackend:
			t.buildBackendSend(input, send)
		case MessageTransient:
			t.buildTransientSend(input, send)
		default:
			glog.Warningln("didn't find protocol handler", send.Protocol)
			return nil
		}
	}
	return sends
}

func (t *Transition) buildBackendSend(input *Event, send *Event) {
	var inputEventSID, sendEventSID, sessionID string

	if input.Backend != nil {
		inputEventSID = input.Backend.SessionID
		glog.V(3).Infoln("input", input.Backend.Content, inputEventSID)
	}
	if send != nil && send.EventData != nil && send.Backend != nil {
		sendEventSID = send.Backend.SessionID
		glog.V(3).Infoln("send", send.Backend.Content, sendEventSID)
	}
	if inputEventSID == "" {
		sessionID = sendEventSID
	}
	glog.V(5).Infoln("send.Rule:", send.Rule)
	glog.V(5).Infof("Data: '%v'", t.Trigger.Data)
	if sessionID == "" {
		sessionID = t.Machine.Memory[LUA_SESSION_ID]
		glog.V(1).Infoln("trying to get SessionID from memory", sessionID)
	}
	glog.V(5).Infof("sessionID: '%v'", sessionID)
	content := ""
	connID := t.Machine.ConnID
	switch send.Rule {
	case TriggerTypeLua:
		out, _, ok := send.ExecLua(input.Data, LUA_ALL_OK)
		if ok {
			content = out
		} else {
			content = input.Data
		}
	case TriggerTypeData:
		content = send.Data
	case TriggerTypeUseInput:
		dataStr := ""
		if input.ProtocolType == agency.Protocol_BASIC_MESSAGE {
			dataStr = input.EventData.BasicMessage.Content
		} else {
			glog.V(2).Infoln("+++ build backend send: not BM")
		}
		glog.V(2).Infoln("+++ dataStr:", dataStr)
		content = dataStr
	case TriggerTypeFormat:
		content = fmt.Sprintf(send.Data, input.Data)
	case TriggerTypeFormatFromMem:
		content = t.FmtFromMem(send)
	}

	send.EventData = &EventData{Backend: &BackendData{
		Content:   content,
		SessionID: sessionID,
		ConnID:    connID,
	}}
}

func (t *Transition) buildTransientSend(_ *Event, send *Event) {
	switch send.Rule {
	case TriggerTypeTransient:
		send.EventData = &EventData{BasicMessage: &BasicMessage{
			Content: send.Data,
		}}
	default:
		assert.Equal(send.Rule, TriggerTypeTransient, "only Transients are supported")
	}
}

func (t *Transition) buildHookSend(input *Event, send *Event) {
	switch send.Rule {
	case TriggerTypeData:
		send.EventData = &EventData{Hook: &Hook{
			Data: map[string]string{
				"ID":   send.TypeID,
				"data": send.Data,
			},
		}}
	case TriggerTypeUseInput:
		dataStr := ""
		if input.ProtocolType == agency.Protocol_BASIC_MESSAGE {
			dataStr = input.EventData.BasicMessage.Content
		}
		send.EventData = &EventData{Hook: &Hook{
			Data: map[string]string{
				"ID":   send.TypeID,
				"data": dataStr,
			},
		}}
	case TriggerTypeFormat:
		send.EventData = &EventData{Hook: &Hook{
			Data: map[string]string{
				"ID":   send.TypeID,
				"data": fmt.Sprintf(send.Data, input.Data),
			},
		}}
	case TriggerTypeFormatFromMem:
		send.EventData = &EventData{Hook: &Hook{
			Data: map[string]string{
				"ID":   send.TypeID,
				"data": t.FmtFromMem(send),
			},
		}}
	}
}

func (t *Transition) buildBMSend(input *Event, send *Event) {
	assert.That(input != nil ||
		send.Rule == TriggerTypeData ||
		send.Rule == TriggerTypeFormatFromMem,
	)
	switch send.Rule {
	case TriggerTypeUseInput:
		send.EventData = input.EventData
	case TriggerTypeData:
		send.EventData = &EventData{BasicMessage: &BasicMessage{
			Content: send.Data,
		}}
	case TriggerTypeFormat:
		send.EventData = &EventData{BasicMessage: &BasicMessage{
			Content: fmt.Sprintf(send.Data, input.Data),
		}}
	case TriggerTypeFormatFromMem:
		send.EventData = &EventData{BasicMessage: &BasicMessage{
			Content: t.FmtFromMem(send),
		}}
	case TriggerTypeLua:
		content := input.Data
		out, _, ok := send.ExecLua(content, LUA_ALL_OK)
		if ok {
			send.EventData = &EventData{BasicMessage: &BasicMessage{
				Content: out,
			}}
		} else {
			send.EventData = &EventData{BasicMessage: &BasicMessage{
				Content: content,
			}}
		}
	}
}

func (t *Transition) buildInputEvent(status *agency.ProtocolStatus) (e *Event) {
	if status == nil {
		return nil
	}
	e = &Event{
		Protocol:       toFileProtocolType[status.GetState().ProtocolID.TypeID],
		ProtocolType:   status.GetState().ProtocolID.TypeID,
		ProtocolStatus: status,
	}
	switch status.GetState().ProtocolID.TypeID {
	case agency.Protocol_ISSUE_CREDENTIAL, agency.Protocol_PRESENT_PROOF:
		switch t.Trigger.Rule {
		case TriggerTypeOurMessage:
			glog.V(4).Infoln("+++ Our message:", status.GetState().ProtocolID.TypeID)
			return e
		}
	case agency.Protocol_DIDEXCHANGE:
		return e
	case agency.Protocol_BASIC_MESSAGE:
		content := status.GetBasicMessage().Content
		switch t.Trigger.Rule {
		case TriggerTypeValidateInputNotEqual, TriggerTypeValidateInputEqual,
			TriggerTypeLua, TriggerTypeUseInput, TriggerTypeTransient:
			e.Data = content
			e.EventData = &EventData{BasicMessage: &BasicMessage{
				Content: content,
			}}

			// this isn't so important for f-fsm, or useful yet
		case TriggerTypeUseInputSaveConnID:
			t.Machine.Memory[t.Machine.ConnID+t.Trigger.Data] = content
			e.Data = content
			e.EventData = &EventData{BasicMessage: &BasicMessage{
				Content: content,
			}}

		case TriggerTypeUseInputSaveSessionID:
			sessionID := content
			glog.V(2).Infoln("-- save session id:", sessionID)
			t.Machine.Memory[LUA_SESSION_ID] = sessionID
			e.Data = sessionID
			e.EventData = &EventData{BasicMessage: &BasicMessage{
				Content: sessionID,
			}}

		case TriggerTypeUseInputSave:
			t.Machine.Memory[t.Trigger.Data] = content
			e.Data = content
			e.EventData = &EventData{BasicMessage: &BasicMessage{
				Content: content,
			}}
		case TriggerTypeData, TriggerTypeInputEqual:
			e.EventData = &EventData{BasicMessage: &BasicMessage{
				Content: t.Trigger.Data,
			}}
		}
	}
	return e
}

func (t *Transition) buildInputAnswers(status *agency.AgentStatus) (e *Event) {
	e = &Event{
		Protocol:     toFileProtocolType[status.Notification.ProtocolType],
		ProtocolType: status.Notification.ProtocolType,
	}
	return e
}

func (t *Transition) FmtFromMem(send *Event) string {
	defer err2.Catch(err2.Err(func(err error) {
		glog.Error(err)
	}))
	tmpl := template.Must(template.New("template").Parse(send.Data))
	var buf bytes.Buffer
	try.To(tmpl.Execute(&buf, t.Machine.Memory))
	return buf.String()
}

func (t *Transition) withNewTarget(tgt string) (nt *Transition) {
	if tgt == "" {
		return t
	}
	nt = new(Transition)
	*nt = *t
	nt.Target = tgt
	return nt
}

func pin(digit int) int {
	min := int(math.Pow(10, float64(digit-1)))
	max := int(math.Pow(10, float64(digit)))
	return min + rand.Intn(max-min)
}

func (t *Transition) GenPIN(_ *Event) {
	t.Machine.Memory["PIN"] = fmt.Sprintf("%v", pin(digitsInPIN))
	glog.V(1).Infoln("pin code:", t.Machine.Memory["PIN"])
}

func (t *Transition) BuildSendAnswers(status *agency.AgentStatus) []*Event {
	input := t.buildInputAnswers(status)
	return t.doBuildSendEvents(input)
}
