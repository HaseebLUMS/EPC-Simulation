package rpcs

// RemoteMME - Students should not use this interface in their code. Use WrapMME() instead.
type RemoteMME interface {
	RecvUERequest(args *UERequestArgs, reply *MMEUERequestReply) error
	RecvMMEStats(args *MMEStatsArgs, reply *MMEStatsReply) error
	// TODO: add additional RPC signatures below!
	RecvUpdate(args UpdateArgs, reply *UpdateReply) error
	RecvTriggerForReplicas(args TriggerArgs, reply *TriggerReply) error
	RecvBackup(args Backup, reply *Backup) error
}

// RemoteLoadBalancer - Students should not use this interface in their code. Use WrapLB() instead.
type RemoteLoadBalancer interface {
	RecvUERequest(args *UERequestArgs, reply *UERequestReply) error
	RecvLeave(args *LeaveArgs, reply *LeaveReply) error
	RecvLBStats(args *LBStatsArgs, reply *LBStatsReply) error
	RecvJoin(args *JoinArgs, reply *JoinReply) error
	RecvReBalance(arsg JoinArgs, reply *JoinReply) error
}

// MME ...
type MME struct {
	// Embed all methods into the struct. See the Effective Go section about
	// embedding for more details: golang.org/doc/effective_go.html#embedding
	RemoteMME
}

// LoadBalancer ...
type LoadBalancer struct {
	// Embed all methods into the struct. See the Effective Go section about
	// embedding for more details: golang.org/doc/effective_go.html#embedding
	RemoteLoadBalancer
}

// WrapMME wraps t in a type-safe wrapper struct to ensure that only the desired
// methods are exported to receive RPCs. Any other methods already in the
// input struct are protected from receiving RPCs.
func WrapMME(t RemoteMME) RemoteMME {
	return &MME{t}
}

// WrapLoadBalancer wraps t in a type-safe wrapper struct to ensure that only the desired
// methods are exported to receive RPCs. Any other methods already in the
// input struct are protected from receiving RPCs.
func WrapLoadBalancer(t RemoteLoadBalancer) RemoteLoadBalancer {
	return &LoadBalancer{t}
}
