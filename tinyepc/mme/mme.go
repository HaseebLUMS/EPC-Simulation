package mme

/*
	IMPLEMENTATION:
		MME node is responsible for storing state of users assigned to it, updating it and catering to user requests
		--> Upon creation, MME node sends join request to Load Balancer and triggers relocation mechanism on Load Balancer
		--> Then it recieves state containing keys allocated to this node and its virtual node, and replicas assigned to it.
		--> When a user request comes in, MME node is responsible for updating the state
		--> If node has to leave, it again sends keys assigned to it to the node and sends in a Leave message to load balancer.
*/

import (
	"fmt"
	"net"
	"net/http"
	"net/rpc"
	"tinyepc/rpcs"
)

// mme defines different State variables for MME node
type mme struct {
	ID             uint64
	ln             *net.Listener
	lbClinet       *rpc.Client
	UEState        map[uint64]rpcs.MMEState
	numServed      int
	replicas       []string
	keysToReplicas map[uint64][]string
	semaphore      chan int
	port           string
}

// New creates and returns (but does not start) a new MME.
func New() MME {
	var m MME = new(mme)
	return m
}

func (m *mme) Close() {
	(*m.ln).Close()
}

// Registers MME nodes with RPC and runs go routine for server
func spinRPCServer(m *mme, ln *net.Listener) error {
	rpcServer := rpc.NewServer()
	rpcServer.Register(rpcs.WrapMME(m))
	http.DefaultServeMux = http.NewServeMux()
	rpcServer.HandleHTTP(rpc.DefaultRPCPath, rpc.DefaultDebugPath)
	go http.Serve(*ln, nil)
	return nil
}

// StartMME starts MME node: creates connection with Load balancer, sends join request and recieves share of keys and replicas
func (m *mme) StartMME(hostPort string, loadBalancer string) error {

	m.UEState = make(map[uint64]rpcs.MMEState)
	m.semaphore = make(chan int, 1)
	m.replicas = make([]string, 0)
	m.numServed = 0
	m.keysToReplicas = make(map[uint64][]string)
	m.port = hostPort
	//Listening on hostport
	lbAddr := "localhost" + loadBalancer
	addr := "localhost" + hostPort
	ln, err := net.Listen("tcp", addr)
	m.ln = &ln
	if err != nil {
		fmt.Println("Listener error", err)
		return err
	}

	//Running RPC Server on listner ln
	spinRPCServer(m, &ln)

	//Dialling to load balancer on lbAddr
	client, err := rpc.DialHTTP("tcp", lbAddr)
	if err != nil {
		fmt.Println("MME couldn't dial to load balancer")
	}

	m.lbClinet = client
	//Making RPC call for joining ring
	args := rpcs.JoinArgs{MMEPort: hostPort}
	reply := new(rpcs.JoinReply)
	err = client.Call("LoadBalancer.RecvJoin", args, &reply)
	m.ID = reply.HashedKey
	m.keysToReplicas = reply.KeysToReplicas
	for _, v := range reply.KeysToReplicas {
		for _, rep := range v {
			m.replicas = append(m.replicas, rep)
		}
	}

	// RPC call to initiate re-balancing procedure on Load Balancer
	err = client.Call("LoadBalancer.RecvReBalance", args, reply)
	if err != nil {
		fmt.Println("LoadBalancer.ReBalance call failed", err)
	}
	return nil
}

// RecvUERequest caters user request of SMS, Call and Load
func (m *mme) RecvUERequest(args *rpcs.UERequestArgs, reply *rpcs.MMEUERequestReply) error {
	m.semaphore <- 1
	_, exists := m.UEState[args.UserID]
	if !exists {
		m.UEState[args.UserID] = rpcs.MMEState{Balance: (100)}
	}
	prev := m.UEState[args.UserID]
	if args.UEOperation == rpcs.SMS {
		m.UEState[args.UserID] = rpcs.MMEState{Balance: prev.Balance - (1)}
	} else if args.UEOperation == rpcs.Call {
		m.UEState[args.UserID] = rpcs.MMEState{Balance: prev.Balance - (5)}
	} else if args.UEOperation == rpcs.Load {
		m.UEState[args.UserID] = rpcs.MMEState{Balance: prev.Balance + (10)}
	}
	m.numServed++
	reply.MMEId = m.ID
	reply.State = m.UEState[args.UserID]
	<-m.semaphore
	return nil
}

// RecvMMEStats is called by the tests to fetch MME state information
func (m *mme) RecvMMEStats(args *rpcs.MMEStatsArgs, reply *rpcs.MMEStatsReply) error {
	m.semaphore <- 1
	reply.NumServed = m.numServed
	reply.Replicas = m.replicas
	reply.State = m.UEState
	reps := make([]string, 0)
	uiqueReplicas := make(map[string]bool)
	for _, r := range m.replicas {
		uiqueReplicas[r] = true
	}
	for k := range uiqueReplicas {
		reps = append(reps, k)
	}
	reply.Replicas = reps
	<-m.semaphore
	return nil
}

// RecvTriggerForReplicas Returns replicas to load balancer and clears its own state
func (m *mme) RecvTriggerForReplicas(args rpcs.TriggerArgs, reply *rpcs.TriggerReply) error {
	reply.UEState = make(map[uint64]rpcs.MMEState)
	for k, v := range m.UEState {
		reply.UEState[k] = v
	}
	reply.KeysToReplicas = make(map[uint64][]string)
	for k, v := range m.keysToReplicas {
		reply.KeysToReplicas[k] = v

	}

	//Clearing the old state as new will be received
	//by RecvUpdate
	tmp := make(map[uint64]rpcs.MMEState)
	m.UEState = tmp
	tmp2 := make([]string, 0)
	m.replicas = tmp2
	tmp3 := make(map[uint64][]string)
	m.keysToReplicas = tmp3

	return nil
}

//RecvUpdate Updates replicas and keys as specified by Load Balancer after join/leave
func (m *mme) RecvUpdate(args rpcs.UpdateArgs, reply *rpcs.UpdateReply) error {
	reps := make([]string, 0)
	if args.Replica != "" {
		reps = append(reps, args.Replica)
		m.replicas = append(m.replicas, reps[0])
	}
	m.keysToReplicas[args.Key] = reps
	for k, v := range args.UEState {
		m.UEState[k] = v
	}
	return nil
}

// RecvBackup For state synchronization
func (m *mme) RecvBackup(args rpcs.Backup, reply *rpcs.Backup) error {
	m.UEState[args.CliID] = args.State
	return nil
}
