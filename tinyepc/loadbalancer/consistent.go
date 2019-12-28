package loadbalancer

/*
	IMPLEMENTATION:
		We handle hash related calculations here. Based on hash, we find
		--> Responsible MME node for a key
		--> Find replicas of a provided node
		--> Maintaining state for load balancer such as number of ring node and physical nodes, map from hash to ports
*/

import (
	"crypto/sha256"
	"encoding/binary"
	"sort"
	"strconv"
)

//nodeStatus tells whether a node is Physical or Virtual
type nodeStatus int

const (
	physical nodeStatus = iota
	virtual
)

// ConsistentHashing contains any fields that are required to maintain
// the consistent hash ring
type ConsistentHashing struct {
	//Do not forget to initiaze the declared funcks
	MMENodes          map[uint64]string //hashedKey to address
	nodesStatus       map[uint64]nodeStatus
	virtualToPhysical map[uint64]uint64
	ringNodes         int
	physicalNodes     int
}

// Hash calculates hash of the input key using SHA-256. DO NOT MODIFY!
func (c *ConsistentHashing) Hash(key string) uint64 {
	hasher := sha256.New()
	hasher.Write([]byte(key))
	digest := hasher.Sum(nil)
	digestAsUint64 := binary.LittleEndian.Uint64(digest)
	return digestAsUint64
}

//getReplicas is a pure function which only finds next clockwise
//neighbor of key and it has no side effects
func (c *ConsistentHashing) getReplicas(key uint64) []string {
	//res for storing results
	res := make([]string, 0)

	//get the nodes (keys of c.MMENodes) and sort the results
	nodes := make([]uint64, 0)
	for k := range c.MMENodes {
		nodes = append(nodes, k)
	}
	sort.Slice(nodes, func(i, j int) bool {
		return nodes[i] < nodes[j]
	})

	keys := make(map[uint64]bool)
	keys[key] = true
	/*Store replicas
	in keys map for handling virtual nodes*/

	//fill the res list
	pickNext := false
	firstPhysicalNode := uint64(0)
	firstPhysicalNodeSelected := false
	for _, i := range nodes {
		//select first physical node that will become replica if no
		//replica is found in nodes array (i.e replica of last physcal node is nodes)
		if (!firstPhysicalNodeSelected) && c.virtualToPhysical[i] == i {
			firstPhysicalNode = i
			firstPhysicalNodeSelected = true
		}
		//checks if the replica is not a virtual node of node itself
		if (pickNext == true) && (c.virtualToPhysical[i] != key) {
			res = append(res, c.MMENodes[i])
			pickNext = false
		}
		_, exists := keys[i]
		if exists {
			pickNext = true
		}
	}
	if pickNext == true && len(nodes) > 1 {
		res = append(res, c.MMENodes[firstPhysicalNode])
	}

	return res
}

//InsertMME inserts MME and returns replicas
func (c *ConsistentHashing) InsertMME(val string, weight int) (uint64, map[uint64][]string) {
	res := make(map[uint64][]string)

	//Making a physical node
	key := c.Hash(val)
	c.MMENodes[key] = val
	c.nodesStatus[key] = physical
	c.virtualToPhysical[key] = key
	c.physicalNodes++
	c.ringNodes++

	replicas := c.getReplicas(key)
	res[key] = replicas
	if weight == 1 {
		return key, res
	}

	//Making virtual nodes
	for i := 1; i < weight; i++ {
		virtualKey := c.VirtualNodeHash(val, i)
		c.MMENodes[virtualKey] = val
		c.nodesStatus[virtualKey] = virtual
		c.virtualToPhysical[virtualKey] = key
		c.ringNodes++
		replicasForVirtual := c.getReplicas(virtualKey)
		res[virtualKey] = replicasForVirtual
	}
	return key, res
}

//GetState to get LB state
func (c *ConsistentHashing) GetState() (int, int, []uint64, []string) {
	hashes := make([]uint64, 0)
	servers := make([]string, 0)
	for k := range c.MMENodes {
		hashes = append(hashes, k)
	}
	sort.Slice(hashes, func(i, j int) bool {
		return hashes[i] < hashes[j]
	})
	for _, k := range hashes {
		servers = append(servers, c.MMENodes[k])
	}
	return c.ringNodes, c.physicalNodes, hashes, servers
}

//GetMMENode returns appropriate MME node responsible for the given key.
func (c *ConsistentHashing) GetMMENode(key uint64) string {
	keys := make([]uint64, 0)
	for k := range c.MMENodes {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool {
		return keys[i] < keys[j]
	})
	found := false
	var placementNode uint64 = 0
	for _, k := range keys {
		if key <= k {
			found = true
			placementNode = k
			break
		}
	}
	if !found {
		placementNode = keys[0]
	}
	return c.MMENodes[placementNode]
}

// VirtualNodeHash returns the hash of the nth virtual node of a given
// physical node. DO NOT MODIFY!
//
// Parameters:  key - physical node's key
// 				n - nth virtual node (Possible values: 1, 2, 3, etc.)
func (c *ConsistentHashing) VirtualNodeHash(key string, n int) uint64 {
	return c.Hash(strconv.Itoa(n) + "-" + key)
}
