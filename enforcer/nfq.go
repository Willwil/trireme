package enforcer

// Go libraries
import (
	"fmt"
	"strconv"

	nfqueue "github.com/aporeto-inc/nfqueue-go"
	"github.com/aporeto-inc/trireme/enforcer/utils/packet"
	"go.uber.org/zap"
)

// startNetworkInterceptor will the process that processes  packets from the network
// Still has one more copy than needed. Can be improved.
func (d *Datapath) startNetworkInterceptor() {
	var err error
	d.netStop = make([]chan bool, d.filterQueue.NumberOfNetworkQueues)
	for i := uint16(0); i < d.filterQueue.NumberOfNetworkQueues; i++ {
		d.netStop[i] = make(chan bool)
	}

	nfq := make([]nfqueue.Verdict, d.filterQueue.NumberOfNetworkQueues)

	for i := uint16(0); i < d.filterQueue.NumberOfNetworkQueues; i++ {

		// Initialize all the queues
		nfq[i], err = nfqueue.CreateAndStartNfQueue(d.filterQueue.NetworkQueue+i, d.filterQueue.NetworkQueueSize, nfqueue.NfDefaultPacketSize, nil)
		if err != nil {
			zap.L().Fatal("Unable to initialize netfilter queue", zap.Error(err))
		}

		go func(j uint16) {
			for {
				select {
				case packet := <-nfq[j].GetNotificationChannel():
					d.processNetworkPacketsFromNFQ(packet)
				case <-d.netStop[j]:
					return
				}
			}
		}(i)

	}
}

// startApplicationInterceptor will create a interceptor that processes
// packets originated from a local application
func (d *Datapath) startApplicationInterceptor() {

	var err error
	d.appStop = make([]chan bool, d.filterQueue.NumberOfApplicationQueues)
	for i := uint16(0); i < d.filterQueue.NumberOfApplicationQueues; i++ {
		d.appStop[i] = make(chan bool)
	}

	nfq := make([]nfqueue.Verdict, d.filterQueue.NumberOfApplicationQueues)

	for i := uint16(0); i < d.filterQueue.NumberOfApplicationQueues; i++ {
		nfq[i], err = nfqueue.CreateAndStartNfQueue(d.filterQueue.ApplicationQueue+i, d.filterQueue.ApplicationQueueSize, nfqueue.NfDefaultPacketSize, nil)

		if err != nil {
			zap.L().Fatal("Unable to initialize netfilter queue", zap.Error(err))
		}

		go func(j uint16) {
			for {
				select {
				case packet := <-nfq[j].GetNotificationChannel():
					d.processApplicationPacketsFromNFQ(packet)
				case <-d.appStop[j]:
					return
				}
			}
		}(i)
	}
}

// processNetworkPacketsFromNFQ processes packets arriving from the network in an NF queue
func (d *Datapath) processNetworkPacketsFromNFQ(p *nfqueue.NFPacket) {

	d.net.IncomingPackets++

	// Parse the packet - drop if parsing fails
	netPacket, err := packet.New(packet.PacketTypeNetwork, p.Buffer, strconv.Itoa(int(p.Mark)))

	if err != nil {
		d.net.CreateDropPackets++
		netPacket.Print(packet.PacketFailureCreate)
	} else if netPacket.IPProto == packet.IPProtocolTCP {
		err = d.processNetworkTCPPackets(netPacket)
	} else {
		d.net.ProtocolDropPackets++
		err = fmt.Errorf("Invalid IP Protocol %d", netPacket.IPProto)
	}
	if err != nil {
		length := uint32(len(p.Buffer))
		buffer := p.Buffer
		p.QueueHandle.SetVerdict2(uint32(p.QueueHandle.QueueNum), 0, uint32(p.Mark), length, uint32(p.ID), buffer)
		return
	}

	// // Accept the packet
	length := uint32(0)
	buffer := netPacket.Buffer
	buffer = append(buffer, netPacket.GetTCPOptions()...)
	buffer = append(buffer, netPacket.GetTCPData()...)
	length = uint32(len(buffer))
	p.QueueHandle.SetVerdict2(uint32(p.QueueHandle.QueueNum), 1, uint32(p.Mark), length, uint32(p.ID), buffer)

}

// processApplicationPackets processes packets arriving from an application and are destined to the network
func (d *Datapath) processApplicationPacketsFromNFQ(p *nfqueue.NFPacket) {

	d.app.IncomingPackets++

	// Being liberal on what we transmit - malformed TCP packets are let go
	// We are strict on what we accept on the other side, but we don't block
	// lots of things at the ingress to the network
	appPacket, err := packet.New(packet.PacketTypeApplication, p.Buffer, strconv.Itoa(int(p.Mark)))

	if err != nil {
		d.app.CreateDropPackets++
		appPacket.Print(packet.PacketFailureCreate)
	} else if appPacket.IPProto == packet.IPProtocolTCP {
		err = d.processApplicationTCPPackets(appPacket)
	} else {
		d.app.ProtocolDropPackets++
		err = fmt.Errorf("Invalid IP Protocol %d", appPacket.IPProto)
	}

	if err != nil {
		length := uint32(len(p.Buffer))
		buffer := p.Buffer
		p.QueueHandle.SetVerdict2(uint32(p.QueueHandle.QueueNum), 0, uint32(p.Mark), length, uint32(p.ID), buffer)
		return
	}

	// // // Accept the packet
	length := uint32(0)
	buffer := appPacket.Buffer
	buffer = append(buffer, appPacket.GetTCPOptions()...)
	buffer = append(buffer, appPacket.GetTCPData()...)
	length = uint32(len(buffer))

	p.QueueHandle.SetVerdict2(uint32(p.QueueHandle.QueueNum), 1, uint32(p.Mark), length, uint32(p.ID), buffer)

}