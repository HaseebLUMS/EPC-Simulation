package loadbalancer

/*
	IMPLEMENTATION:
		Load balancer is responsible for distributing keys on different MME nodes, transfering user requests and handling failure cases
		--> When load balancer is started, it creates server and starts listening to incoming connections
		--> Upon recieving join request, load balancer finds replicas of the node and sends it to that MME node
		--> After join request, key relocation mechanism is triggered which gets state from all the nodes and redistribute the keys
		--> Upon receiving user request, load balancer forwards request to the MME node and sends back  the response to user.
		--> Upon receiving leave message, node again fetches and redistibute keys to all mme nodes, replicas are also updated.

*/

import (
	"fmt"
	"net"
	"net/http"
	"net/rpc"
	"sort"
	"strconv"
	"tinyepc/rpcs"
)

// State variables for loadBalancer
type loadBalancer struct {
	ln             *net.Listener
	hashing        *ConsistentHashing
	conns          map[string]*rpc.Client
	weight         int
	semaphore      chan int
	rangeMap       map[uint64]rpcs.Range
	nodeToReplicas map[uint64]string
}

// New returns a new instance of LoadBalancer, but does not start it
func New(ringWeight int) LoadBalancer {
	lb := new(loadBalancer)
	var consistentHashing = new(ConsistentHashing)
	lb.conns = make(map[string]*rpc.Client)
	lb.weight = ringWeight
	lb.rangeMap = make(map[uint64]rpcs.Range)
	lb.nodeToReplicas = make(map[uint64]string)
	lb.hashing = consistentHashing
	lb.hashing.MMENodes = make(map[uint64]string)
	lb.hashing.nodesStatus = make(map[uint64]nodeStatus)
	lb.hashing.virtualToPhysical = make(map[uint64]uint64)
	lb.hashing.ringNodes = 0
	lb.hashing.physicalNodes = 0
	lb.semaphore = make(chan int, 1)
	return lb
}

// Registers rpc call for loadbalancer and creates go routine for load balancer server
func spinRPCServer(lb *loadBalancer, ln *net.Listener) error {
	rpcServer := rpc.NewServer()
	rpcServer.Register(rpcs.WrapLoadBalancer(lb))
	http.DefaultServeMux = http.NewServeMux()
	rpcServer.HandleHTTP(rpc.DefaultRPCPath, rpc.DefaultDebugPath)
	go func() {
		err := http.Serve(*ln, nil)
		_ = err
	}()
	return nil
}

// StartLB creates listener and triggers call to spinRPCServer
func (lb *loadBalancer) StartLB(port int) error {
	//Listening on port
	addr := "localhost:" + strconv.Itoa(port)
	ln, err := net.Listen("tcp", addr)
	lb.ln = &ln
	if err != nil {
		fmt.Println("Listener error", err)
		return err
	}
	//Running RPC server
	spinRPCServer(lb, &ln)

	return nil
}

func (lb *loadBalancer) Close() {
	(*lb.ln).Close()
}

//RecvJoin finds position of joining node using hash and sends it its replicas
func (lb *loadBalancer) RecvJoin(args *rpcs.JoinArgs, reply *rpcs.JoinReply) error {
	lb.semaphore <- 1
	key, keysToReplicas := lb.hashing.InsertMME(args.MMEPort, lb.weight)
	reply.HashedKey = key
	reply.KeysToReplicas = keysToReplicas
	client, err := rpc.DialHTTP("tcp", "localhost"+args.MMEPort)
	if err != nil {
		fmt.Println("MME could not be contacted", err)
		<-lb.semaphore
		return err
	}
	lb.conns[args.MMEPort] = client
	<-lb.semaphore
	return nil
}

// RecvUERequest recieves user request and forwards it to responsible mme node
func (lb *loadBalancer) RecvUERequest(args *rpcs.UERequestArgs, reply *rpcs.UERequestReply) error {
	lb.semaphore <- 1
	args.UserID = lb.hashing.Hash(strconv.Itoa(int(args.UserID)))
	placementNode := lb.hashing.GetMMENode(args.UserID)
	rep := new(rpcs.MMEUERequestReply)
	err := lb.conns[placementNode].Call("MME.RecvUERequest", args, &rep)
	if err != nil {
		fmt.Println("Call to MME RecvUERequest failed.", err)
		<-lb.semaphore
		return err
	}
	<-lb.semaphore
	return nil
}

// RecvLeave upon recieving leave node triggers call for rebalancing
func (lb *loadBalancer) RecvLeave(args *rpcs.LeaveArgs, reply *rpcs.LeaveReply) error {
	UEStateStore, nodesKeysStore := triggerGetReplicasAndStateAtMMEs(lb, args.HostPort)
	processStateAndPipeToMMEs(lb, UEStateStore, nodesKeysStore)
	return nil
}

// RecvLBStats is called by the tests to fetch LB state information
func (lb *loadBalancer) RecvLBStats(args *rpcs.LBStatsArgs, reply *rpcs.LBStatsReply) error {
	lb.semaphore <- 1
	ringNodes, physicalNodes, hashes, servers := lb.hashing.GetState()
	reply.Hashes = hashes
	reply.PhysicalNodes = physicalNodes
	reply.RingNodes = ringNodes
	reply.ServerNames = servers
	<-lb.semaphore
	return nil
}

// RecvReBalance is triggered by a new joining node, responsible for remapping of keys
func (lb *loadBalancer) RecvReBalance(args rpcs.JoinArgs, reply *rpcs.JoinReply) error {
	UEStateStore, nodesKeysStore := triggerGetReplicasAndStateAtMMEs(lb, "")
	processStateAndPipeToMMEs(lb, UEStateStore, nodesKeysStore)
	return nil
}

// triggerGetReplicasAndStateAtMMEs gets state from all the nodes, finds appropriate position for keys. If node is leaving, that node is removed from list of available nodes before rehashing
func triggerGetReplicasAndStateAtMMEs(lb *loadBalancer, leavingNodeAddr string) (map[uint64]rpcs.MMEState, map[uint64]*rpc.Client) {
	lb.semaphore <- 1
	UEStateStore := make(map[uint64]rpcs.MMEState)
	nodesKeysStore := make(map[uint64](*rpc.Client))
	for k, v := range lb.conns {
		args := rpcs.TriggerArgs{}
		rep := new(rpcs.TriggerReply)
		// Call to each node to send in state
		err := v.Call("MME.RecvTriggerForReplicas", args, &rep)
		if err != nil {
			fmt.Println("Couldn't connect to ", k, " in triggerGetReplicasAndStateAtMMEs", err)
			<-lb.semaphore
			return nil, nil
		}
		// storing all key,value pairs in server side state temporarily
		for userKey, userState := range rep.UEState {
			UEStateStore[userKey] = userState
		}
	}
	// If node is leaving, remove it from state
	newMMENodes := make(map[uint64]string)
	for k, v := range lb.hashing.MMENodes {
		if lb.hashing.virtualToPhysical[k] != lb.hashing.Hash(leavingNodeAddr) {
			newMMENodes[k] = v
			nodesKeysStore[k] = nil
		}
	}
	lb.hashing.MMENodes = nil
	lb.hashing.MMENodes = newMMENodes
	<-lb.semaphore
	return UEStateStore, nodesKeysStore
}

// processStateAndPipeToMMEs redistributes keys on servers and sends them to respective mme nodes
func processStateAndPipeToMMEs(lb *loadBalancer, UEStateStore map[uint64]rpcs.MMEState, NodesKeysStore map[uint64]*rpc.Client) {
	lb.semaphore <- 1
	nodes := make([]uint64, 0)
	for k := range NodesKeysStore {
		nodes = append(nodes, k)
	}
	sort.Slice(nodes, func(i, j int) bool {
		return nodes[i] < nodes[j]
	})

	//Calculating state share for each node
	nodeToStates := make(map[uint64]map[uint64]rpcs.MMEState) //MME hash -> (User's hashes -> User States)
	for i := 0; i < len(nodes); i++ {
		node := nodes[i]
		userKeysToStates := make(map[uint64]rpcs.MMEState)

		minInd := i - 1
		maxInd := i
		if minInd < 0 {
			minInd = len(nodes) - 1
			maxInd = -1
		}
		min := nodes[minInd]
		max := nodes[minInd]
		max = 18446744073709551614
		if maxInd != -1 {
			max = nodes[maxInd]
		}
		lb.rangeMap[node] = rpcs.Range{Min: min, Max: max}
		for user := range UEStateStore {
			if user > min && user <= max {
				userKeysToStates[user] = UEStateStore[user]
			}
		}
		nodeToStates[node] = userKeysToStates
	}

	//calculating replicas for each node
	nodeToReplicas := make(map[uint64]string)
	if len(nodes) > 1 {
		for i := 0; i < len(nodes); i++ {
			replicaFound := false
			for j := i + 1; j < len(nodes); j++ {
				if lb.hashing.MMENodes[nodes[j]] != lb.hashing.MMENodes[nodes[i]] {
					nodeToReplicas[nodes[i]] = lb.hashing.MMENodes[nodes[j]]
					replicaFound = true
					break
				}
			}
			if !replicaFound {
				for j := 0; j < len(nodes); j++ {
					if lb.hashing.MMENodes[nodes[j]] != lb.hashing.MMENodes[nodes[i]] {
						nodeToReplicas[nodes[i]] = lb.hashing.MMENodes[nodes[j]]
						break
					}
				}
			}
		}
	}

	//storing nodeToReplicas
	for k, v := range nodeToReplicas {
		lb.nodeToReplicas[k] = v
	}

	//Sending the updates to each node
	for _, node := range nodes {
		conn := lb.conns[lb.hashing.MMENodes[node]]
		arg := rpcs.UpdateArgs{UEState: nodeToStates[node], Replica: nodeToReplicas[node], Key: node}
		rep := new(rpcs.UpdateReply)
		conn.Call("MME.RecvUpdate", arg, rep)
	}
	<-lb.semaphore
}

//Also send backup states to replicas here.
//And do the leave node part.

// For state synchronization
func replicaIStateOnJNode(nodeToStates map[uint64]map[uint64]rpcs.MMEState, nodeI uint64, nodeJ uint64) {
	for k, v := range nodeToStates[nodeI] {
		nodeToStates[nodeJ][k] = v
	}
	return
}
