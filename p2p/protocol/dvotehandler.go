// Copyright 2018 The go-infinet Authors
// This file is part of the go-infinet library.
//
// The go-infinet library is free software: you can redistribute it and/or modify
// it under the terms of the GNU Lesser General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.
//
// The go-infinet library is distributed in the hope that it will be useful,
// but WITHOUT ANY WARRANTY; without even the implied warranty of
// MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
// GNU Lesser General Public License for more details.
//
// You should have received a copy of the GNU Lesser General Public License
// along with the go-infinet library. If not, see <http://www.gnu.org/licenses/>.


package protocol

import (
	"time"
	"strconv"
	"sync"
	"github.com/juchain/go-juchain/common"
	"github.com/juchain/go-juchain/common/log"
	"github.com/juchain/go-juchain/core"
	"github.com/juchain/go-juchain/p2p/protocol/downloader"
	"github.com/juchain/go-juchain/p2p"
	"github.com/juchain/go-juchain/p2p/discover"
	"github.com/juchain/go-juchain/p2p/node"
	"github.com/juchain/go-juchain/consensus"
	"github.com/juchain/go-juchain/consensus/dpos"
	"github.com/juchain/go-juchain/config"
	"math/rand"
	"fmt"
	"reflect"
)

// DPoS voting handler is purposed on the voting process of all delegators.
// this is the previous process of DPoS delegator packaging process.
// we need to vote at least 31 delegators and 70 candidates in the smart contract.
// if all the conditions satisfied, then activate the delegator packaging process.
// DPoS packaging handler.
// ------------------------
// the voting process of the best node has three stages:
// 1. try to vote for the best node with random tickets to all peers.
// 2. solve the best node confliction if has.
// 3. exchange the voted best node from all peers.
var (
	TestMode          bool   = false; // only for test case.
	PackagingInterval uint32 = 2;     // vote for packaging node in every 5 seconds.
	ElectingInterval  uint32 = 15;    // elect for new node in every 30 seconds.

	ElectionInfo0    *ElectionInfo; // we use two versions of election info for switching election node smoothly.
	NextElectionInfo *ElectionInfo;
	LastElectedNodeId string;
	//enableBNConflict  bool   = false;
	BNConflictInterval uint32 = 4; // must be small than ElectingInterval / 2
)

type ElectionInfo struct {
	round            uint64;
	enodestate       uint8; //= VOTESTATE_LOOKING
	electionTickets  uint32; // default tickets which counts on all peers.
	electionNodeId   string; //= ""
	electionNodeIdHash []byte; // the election node id.

	activeTime         uint64; // active time of next round.
	latestActiveENode  time.Time;// = time.Now(); // the check whether the election node is active or not.
	confirmedTickets map[string]uint32; // confirmed tickets from all peers. <nodeid><tickets>
	confirmedActiveTimes map[string]uint64; // confirmed the next active time from all peers. <nodeid><activetime>
}

type DVoteProtocolManager struct {
	networkId     uint64;
	ethManager    *ProtocolManager;
	blockchain    *core.BlockChain;

	lock          *sync.Mutex; // protects running

	packager      *dpos.Packager;
	dposManager	  *DPoSProtocolManager;

	t1            *time.Timer; // global synchronized timer.

}

// NewProtocolManager returns a new ethereum sub protocol manager. The JuchainService sub protocol manages peers capable
// with the ethereum network.
func NewDVoteProtocolManager(eth *JuchainService, ethManager *ProtocolManager, config *config.ChainConfig, config2 *node.Config,
	mode downloader.SyncMode, networkId uint64, blockchain *core.BlockChain, engine consensus.Engine) (*DVoteProtocolManager, error) {
	// Create the protocol manager with the base fields
	manager := &DVoteProtocolManager{
		networkId:   networkId,
		ethManager:  ethManager,
		blockchain:  blockchain,
		lock:        &sync.Mutex{},
		packager:    dpos.NewPackager(config, engine, DefaultConfig.Etherbase, eth, eth.EventMux()),
	}
	//manager.dposManager
	manager0, err0 := NewDPoSProtocolManager(eth, ethManager, config, config2, mode, networkId, blockchain, engine);
	if err0 != nil {
		return nil, err0;
	}
	manager.dposManager = manager0;
	currNodeId = discover.PubkeyID(&config2.NodeKey().PublicKey).TerminalString();
	currNodeIdHash = common.Hex2Bytes(currNodeId);
	ElectionInfo0 = nil;
	NextElectionInfo = nil;
	return manager, nil
}

func (pm *DVoteProtocolManager) Start(maxPeers int) {
	// get data from contract
	log.Info("Starting DPoS Voting Consensus")
	pm.packager.Start();
	if !TestMode {
		go pm.schedule();
		go pm.scheduleElecting();
	}
}

func (pm *DVoteProtocolManager) schedule() {
	if DelegatorsTable != nil && len(DelegatorsTable) > 2 {
		log.Info("Starting DPoS Delegation Consensus")
		pm.dposManager.Start();
		return;
	}
	pm.schedulePackaging();
}

func (pm *DVoteProtocolManager) schedulePackaging() {
	// generate block by election node.
	if pm.isElectionNode() {
		round := pm.blockchain.CurrentFastBlock().Header().Round;
		block := pm.packager.GenerateNewBlock(round+1, currNodeId);
		block.ToString();
	}
	// confirm broadcasting result.
	time.AfterFunc(time.Second * time.Duration(PackagingInterval), pm.schedule)
}

func (pm *DVoteProtocolManager) isElectionNode() bool {
	return ElectionInfo0 != nil && ElectionInfo0.electionNodeId == currNodeId;
}

func (pm *DVoteProtocolManager) scheduleElecting() {
	pm.lock.Lock()
	defer pm.lock.Unlock()
	DelegatorsTable, DelegatorNodeInfo, _ = VotingAccessor.Refresh();
	if DelegatorsTable != nil && len(DelegatorsTable) > 2 {
		// dpos delegator consensus is activated!
		return;
	}
	pm.t1 = nil;
	round := uint64(1);
	if NextElectionInfo != nil {
		bestNodeId, tickets, activeTime := pm.getBestNodeInfo2()
		if tickets == 0 {
			// 99% should not in here.
			log.Warn("Selecting of the best node is conflicted!")
			//restart the scheduler
			NextElectionInfo = nil;
			go pm.scheduleElecting();
			return;
		}
		bestNodeIdStr := common.Bytes2Hex(bestNodeId)
		if !TestMode {
			gap := int64(activeTime) - time.Now().Unix()
			if gap > 2 || gap < -2 {
				log.Warn(fmt.Sprintf("Scheduling of the new electing round is improper! current gap: %v seconds", gap))
				//restart the scheduler
				NextElectionInfo = nil;
				go pm.scheduleElecting();
				return;
			}
		}
		round = NextElectionInfo.round + 1
		LastElectedNodeId = bestNodeIdStr;

		log.Info(fmt.Sprintf("Confirmed the best election node: %v, activate the new round", bestNodeIdStr));

		ElectionInfo0 = &ElectionInfo{
			NextElectionInfo.round,
			VOTESTATE_SELECTED,
			tickets,
			bestNodeIdStr,
			bestNodeId,
			activeTime,
			NextElectionInfo.latestActiveENode,
			NextElectionInfo.confirmedTickets,
			NextElectionInfo.confirmedActiveTimes,
		};
	}
	NextElectionInfo = &ElectionInfo{
		round,
		VOTESTATE_LOOKING,
		uint32(rand.Intn(100)),
		currNodeId,
		currNodeIdHash,
		uint64(time.Now().Unix() + int64(ElectingInterval)), //UTC time is an universe time. but we need an offset for different country, check here http://tutorials.jenkov.com/java-internationalization/time-zones.html
		time.Now(),
		make(map[string]uint32),
		make(map[string]uint64),
	};
	log.Info(fmt.Sprintf("Elect for next round %v...", round));
	pm.electNodeSafely();

	time.AfterFunc(time.Second * time.Duration(BNConflictInterval), pm.checkBestNodeConflict)
}

// this is a loop function for electing node.
func (pm *DVoteProtocolManager) electNodeSafely() {
	switch NextElectionInfo.enodestate {
	case VOTESTATE_STOP:
		return;
	case VOTESTATE_LOOKING:
		{
			if TestMode { return;}
			// initialize the tickets with the number of all peers connected.
			NextElectionInfo.latestActiveENode = time.Now();
			if uint32(len(pm.ethManager.peers.peers)) == 0 {
				log.Debug("Looking for election node but no any peer found.");
				// we choose rand number as the interval to reduce the conflict while electing.
				time.AfterFunc(time.Second*time.Duration(rand.Intn(5)), pm.electNodeSafely);
				return;
			}
			log.Debug("Start looking for election node with my tickets: " + strconv.Itoa(int(NextElectionInfo.electionTickets)) + " with round: " + strconv.Itoa(int(NextElectionInfo.round)));
			for _, peer := range pm.ethManager.peers.peers {
				err := peer.SendVoteElectionRequest(&VoteElectionRequest{NextElectionInfo.round,
				NextElectionInfo.electionTickets, NextElectionInfo.activeTime, currNodeIdHash});
				if (err != nil) {
					log.Warn("Error occurred while sending VoteElectionRequest: " + err.Error())
				}
			}
			//log.Debug("Start looking for election node... my tickets: " + strconv.Itoa(int(NextElectionInfo.electionTickets)) + ", enodestate: " + strconv.Itoa(int(NextElectionInfo.enodestate)));
			break;
		}
	case VOTESTATE_SELECTED:
		{
			break;
		}
	}
}

func (pm *DVoteProtocolManager) Stop() {
	if DelegatorsTable != nil && len(DelegatorsTable) > 0 {
		pm.dposManager.Stop();
	} else {
		if ElectionInfo0 != nil {
			ElectionInfo0.enodestate = VOTESTATE_STOP;
		}
		if NextElectionInfo != nil {
			NextElectionInfo.enodestate = VOTESTATE_STOP;
		}
		pm.packager.Stop();
	}
	// Quit the sync loop.
	log.Info("DPoS Voting Consensus stopped")
}

// handleMsg is invoked whenever an inbound message is received from a remote
// peer. The remote connection is torn down upon returning any error.
func (pm *DVoteProtocolManager) handleMsg(msg *p2p.Msg, p *peer) error {
	pm.lock.Lock()
	defer pm.lock.Unlock()
	if ElectionInfo0 != nil {
		ElectionInfo0.latestActiveENode = time.Now();
	}
	switch {
	case msg.Code == VOTE_ElectionNode_Request:
		var request VoteElectionRequest;
		if err := msg.Decode(&request); err != nil {
			return errResp(DPOSErrDecode, "%v: %v", msg, err);
		}
		if NextElectionInfo == nil {//sometime happens
			return nil;
		}
		log.Debug(fmt.Sprintf("Received request round %v, nodeid %v, CurrRound %v", request.Round, common.Bytes2Hex(request.NodeId), NextElectionInfo.round));
		if request.Round == NextElectionInfo.round {
			if NextElectionInfo.enodestate == VOTESTATE_SELECTED {
				log.Debug("I am in agreed state " + NextElectionInfo.electionNodeId);
				if TestMode {
					return nil;
				}
				// update the best node.
				nodeId := common.Bytes2Hex(request.NodeId[:8])
				NextElectionInfo.confirmedTickets[nodeId] ++;
				NextElectionInfo.confirmedActiveTimes[nodeId] = request.ActiveTime;
				bestNodeId, tickets, activeTime := pm.getBestNodeInfo()
				pm.setNextRoundTimer(activeTime);
				return p.SendVoteElectionResponse(&VoteElectionResponse{
					NextElectionInfo.round,
					tickets,
					activeTime,
					VOTESTATE_SELECTED,
					bestNodeId});
			} else {
				// this comparision will decide who is the winner.
				// remote win.
				if request.Tickets > NextElectionInfo.electionTickets {
					//update the best counter.
					nodeId := common.Bytes2Hex(request.NodeId[:8])
					NextElectionInfo.confirmedTickets[nodeId] ++;
					NextElectionInfo.confirmedActiveTimes[nodeId] = request.ActiveTime;

					//this candidate have more broadcasting power.
					log.Debug("Agreed the request node as the election node: " + NextElectionInfo.electionNodeId);
				} else {
					//update the best counter
					NextElectionInfo.confirmedTickets[currNodeId] ++;
					NextElectionInfo.confirmedActiveTimes[currNodeId] = NextElectionInfo.activeTime;
					log.Debug("I win！ " + currNodeId);
					// I win. the remote loses broadcasting power.
				}
				//update current state
				NextElectionInfo.enodestate = VOTESTATE_SELECTED;
				if TestMode {
					return nil;
				}
				bestNodeId, tickets, activeTime := pm.getBestNodeInfo()
				pm.setNextRoundTimer(activeTime);//sync the timer.
				// broadcast it to all peers again.
				for _, peer := range pm.ethManager.peers.peers {
					err := peer.SendBroadcastVotedElection(&BroadcastVotedElection{
						NextElectionInfo.round,
						tickets,
						activeTime,
						VOTESTATE_SELECTED,
						bestNodeId,
					});
					if (err != nil) {
						log.Warn("Error occurred while sending VoteElectionRequest: " + err.Error())
					}
				}
			}
		} else if request.Round < NextElectionInfo.round {
			log.Debug(fmt.Sprintf("Mismatched request.round %v, CurrRound %v ", request.Round, NextElectionInfo.round))
			if TestMode {
				return nil;
			}
			bestNodeId, tickets, activeTime := pm.getBestNodeInfo()
			return p.SendVoteElectionResponse(&VoteElectionResponse{
				NextElectionInfo.round,
				tickets,
				activeTime,
				VOTESTATE_MISMATCHED_ROUND,
				bestNodeId});
		} else if request.Round > NextElectionInfo.round {
			if (request.Round - NextElectionInfo.round) == 1 {
				// the most reason could be the round timeframe switching later than this request.
				// but we are continue switching as regular.
			} else {
				// attack happens.

			}
		}
		return nil;
	case msg.Code == VOTE_ElectionNode_Response:
		var response VoteElectionResponse;
		if err := msg.Decode(&response); err != nil {
			return errResp(DPOSErrDecode, "%v: %v", msg, err);
		}
		if NextElectionInfo == nil {//sometime happens
			return nil;
		}
		log.Debug("Received a voted response: " + common.Bytes2Hex(response.ElectionNodeId));
		if response.State == VOTESTATE_SELECTED && NextElectionInfo.round == response.Round {
			nodeId := common.Bytes2Hex(response.ElectionNodeId)
			NextElectionInfo.confirmedTickets[nodeId] ++;
			NextElectionInfo.confirmedActiveTimes[nodeId] = response.ActiveTime;

			bestNodeId, tickets, activeTime := pm.getBestNodeInfo();
			pm.setNextRoundTimer(activeTime);
			if TestMode { return nil;}
			for _, peer := range pm.ethManager.peers.peers {
				err := peer.SendBroadcastVotedElection(&BroadcastVotedElection{
					NextElectionInfo.round,
					tickets,
					activeTime,
					VOTESTATE_SELECTED,
					bestNodeId,
				});
				if (err != nil) {
					log.Warn("Error occurred while sending VoteElectionRequest: " + err.Error())
				}
			}
			return nil;
		} else if response.State == VOTESTATE_MISMATCHED_ROUND && NextElectionInfo.enodestate == VOTESTATE_LOOKING {
			log.Info(fmt.Sprintf("Mismatched round %v, switch to %v and then refresh again", NextElectionInfo.round, response.Round))
			// update round and resend
			NextElectionInfo = &ElectionInfo{
				response.Round,
				VOTESTATE_LOOKING,
				0,
				currNodeId,
				currNodeIdHash,
				response.ActiveTime,
				time.Now(),
				make(map[string]uint32),
				make(map[string]uint64),
			};
			pm.electNodeSafely()
		}
		return nil;
	case msg.Code == VOTE_BESTNODE_CONFLICT:
		var response BroadcastBestNodeConflict;
		if err := msg.Decode(&response); err != nil {
			return errResp(DPOSErrDecode, "%v: %v", msg, err);
		}
		if NextElectionInfo == nil || response.Round != NextElectionInfo.round {
			return nil;
		}
		// simply response the best node.
		bestNodeId, tickets, activeTime := pm.getBestNodeInfo()
		pm.setNextRoundTimer(activeTime);
		if TestMode { return nil;}
		return p.SendVoteElectionResponse(&VoteElectionResponse{
			NextElectionInfo.round,
			tickets,
			activeTime,
			VOTESTATE_SELECTED,
			bestNodeId});
		return nil;
	case msg.Code == VOTE_ElectionNode_Broadcast:
		var response BroadcastVotedElection;
		if err := msg.Decode(&response); err != nil {
			return errResp(DPOSErrDecode, "%v: %v", msg, err);
		}
		if NextElectionInfo == nil || response.Round != NextElectionInfo.round {
			return nil;
		}
		nodeId := common.Bytes2Hex(response.ElectionNodeId)
		log.Debug("Received broadcast message: " + nodeId);
		// just calculate the voted tickets.
		NextElectionInfo.confirmedTickets[nodeId] ++;
		NextElectionInfo.confirmedActiveTimes[nodeId] = response.ActiveTime;

		bestNodeId, _, activeTime := pm.getBestNodeInfo();
		if NextElectionInfo.enodestate == VOTESTATE_SELECTED {
			// check who is the final elected node.
			if !reflect.DeepEqual(NextElectionInfo.electionNodeIdHash, bestNodeId) {
				log.Info(fmt.Sprintf("Switched to the best election node: %v", common.Bytes2Hex(bestNodeId)));
				NextElectionInfo.electionNodeId = common.Bytes2Hex(bestNodeId);
				NextElectionInfo.electionNodeIdHash = bestNodeId;
				NextElectionInfo.activeTime = activeTime;
				pm.setNextRoundTimer(activeTime);
			}
		} else if NextElectionInfo.enodestate == VOTESTATE_LOOKING { //&& maxTickets > uint32(len(pm.ethManager.peers.peers))
			NextElectionInfo.enodestate = VOTESTATE_SELECTED;
			NextElectionInfo.electionNodeId = common.Bytes2Hex(bestNodeId);
			NextElectionInfo.electionNodeIdHash = bestNodeId;
			NextElectionInfo.activeTime = activeTime;
			pm.setNextRoundTimer(activeTime);
		}

		return nil;
	default:
		return pm.dposManager.handleMsg(msg, p)
	}
	return nil
}

func (pm *DVoteProtocolManager) getBestNodeInfo() ([]byte, uint32, uint64) {
	if len(NextElectionInfo.confirmedTickets) == 0 {
		return NextElectionInfo.electionNodeIdHash, NextElectionInfo.electionTickets, NextElectionInfo.activeTime;
	}
	maxTickets, bestNodeId := uint32(0), "";
	for key, value := range NextElectionInfo.confirmedTickets {
		if maxTickets < value {
			maxTickets = value;
			bestNodeId = key;
			// if there are more than two items with the same tickets, lets handle it in getBestNodeInfo2.
		}
	}
	return common.Hex2Bytes(bestNodeId), maxTickets, NextElectionInfo.confirmedActiveTimes[bestNodeId];
}

// get the best node without confliction
func (pm *DVoteProtocolManager) getBestNodeInfo2() ([]byte, uint32, uint64) {
	if len(NextElectionInfo.confirmedTickets) == 0 {
		return NextElectionInfo.electionNodeIdHash, NextElectionInfo.electionTickets, NextElectionInfo.activeTime;
	}
	maxTickets, bestNodeId := uint32(0), "";
	for key, value := range NextElectionInfo.confirmedTickets {
		if maxTickets < value {
			maxTickets = value;
			bestNodeId = key;
		}
	}

	counter := 0
	for _, value := range NextElectionInfo.confirmedTickets {
		if maxTickets == value {
			counter ++;
		}
	}
	// if there are more than two items with the same tickets.
	if counter > 1 {
		return nil, 0, 0
	}
	return common.Hex2Bytes(bestNodeId), maxTickets, NextElectionInfo.confirmedActiveTimes[bestNodeId];
}

func (pm *DVoteProtocolManager) checkBestNodeConflict() {
	if len(NextElectionInfo.confirmedTickets) > 1 {
		_, tickets, _ := pm.getBestNodeInfo2()
		if tickets == 0 {
			log.Warn("Selecting of the best node is conflicted, sync from peers again.")
			// sync from peers.
			for _, peer := range pm.ethManager.peers.peers {
				err := peer.SendBestNodeConflict(&BroadcastBestNodeConflict{NextElectionInfo.round,
					1, NextElectionInfo.activeTime, currNodeIdHash});
				if (err != nil) {
					log.Warn("Error occurred while sending VoteElectionRequest: " + err.Error())
				}
			}
			time.AfterFunc(time.Second * time.Duration(BNConflictInterval), pm.checkBestNodeConflict);
		}
	}

}

func (pm *DVoteProtocolManager) setNextRoundTimer(bestActiveTime uint64) {
	leftTime := int64(bestActiveTime) - time.Now().Unix()
	if leftTime < 1 {
		log.Warn("Discard this round due to the expiration of the active time. reschedule it.")
		go pm.scheduleElecting();
		return;
	}
	if pm.t1 != nil {
		pm.t1.Stop() // potentially could be an issue if the timer is unable to be cancelled.
		log.Debug(fmt.Sprintf("rescheduled for next round %v in %v seconds", NextElectionInfo.round+1, leftTime))
	} else {
		log.Debug(fmt.Sprintf("scheduled for next round %v in %v seconds", NextElectionInfo.round+1, leftTime))
	}
	pm.t1 = time.AfterFunc(time.Second*time.Duration(leftTime), pm.scheduleElecting)
}