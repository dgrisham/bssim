package main

import (
	"sync"
	//"log"
	"database/sql"
	"errors"
	"fmt"
	bs "github.com/heems/bssim/Godeps/_workspace/src/github.com/ipfs/go-ipfs/exchange/bitswap"
	"github.com/heems/bssim/Godeps/_workspace/src/github.com/ipfs/go-ipfs/p2p/peer"
	_ "github.com/heems/bssim/Godeps/_workspace/src/github.com/mattn/go-sqlite3"
	prom "github.com/heems/bssim/Godeps/_workspace/src/github.com/prometheus/client_golang/prometheus"
	"strconv"
	"time"
)

var (
	labels = []string{"latency", "bandwidth", "block_size"}
	//  NewSummaryVec(opts, ["filename", "pid", "latency", "bandwidth"]
	fileTimes = prom.NewHistogramVec(prom.HistogramOpts{
		Name:    "file_times_ms",
		Help:    "Time for peer to get a file.",
		Buckets: prom.LinearBuckets(1, .25, 12),
	}, labels)

	blockTimes = prom.NewHistogramVec(prom.HistogramOpts{
		Name:    "block_times_ms",
		Help:    "Time for peer to get a block.",
		Buckets: prom.ExponentialBuckets(0.005, 10, 10),
		//Buckets: prom.LinearBuckets(0, .05, 100),
	}, labels)

	dupBlocks = prom.NewGaugeVec(prom.GaugeOpts{
		Name: "dup_blocks_count",
		Help: "Count of total duplicate blocks received.",
	}, labels)

	currLables prom.Labels
)

func init() {
	prom.MustRegister(fileTimes)
	prom.MustRegister(blockTimes)
	prom.MustRegister(dupBlocks)
}

type Recorder struct {
	createdAt  time.Time
	currID     int
	times      map[int]time.Time
	rid        int
	currLables []string
	db         *sql.DB
	//  main transaction
	tx *sql.Tx
	//  map of table names to prepared sql statements for them
	stmnts        map[string]*sql.Stmt
	newTimerMutex *sync.Mutex
}

//  assumes configure in main.go has been ran which i should fix
func NewRecorder(dbPath string) *Recorder {
	fmt.Println(dbPath)
	db, err := sql.Open("sqlite3", dbPath)
	check(err)

	s := make(map[string]*sql.Stmt)
	tx, err := db.Begin()
	check(err)

	blockTimesStmt, err := tx.Prepare("INSERT INTO block_times(timestamp, time, runid, peerid) values(?, ?, ?, ?)")
	check(err)

	fileTimesStmt, err := tx.Prepare("INSERT INTO file_times(timestamp, time, runid, peerid, size) values(?, ?, ?, ?, ?)")
	check(err)

	s["block_times"] = blockTimesStmt
	s["file_times"] = fileTimesStmt

	//  get last runid
	var runs sql.NullInt64
	err = db.QueryRow("SELECT MAX(runid) FROM runs").Scan(&runs)
	check(err)
	if !runs.Valid {
		runs.Int64 = 0
	} else {
		//  go to next run
		runs.Int64++
	}

	return &Recorder{
		createdAt:     time.Now(),
		currID:        0,
		times:         make(map[int]time.Time),
		rid:           int(runs.Int64),
		db:            db,
		tx:            tx,
		stmnts:        s,
		newTimerMutex: &sync.Mutex{},
	}
}

//  Closes db without recording half finished run
func (r *Recorder) Kill() {
	r.db.Close()
}

//  Update current run info with duplicate blocks and duration stats
func (r *Recorder) Close(workload string) {
	//  record data here
	duration := time.Since(r.createdAt)
	dup := DupBlocksReceived(peers)

	//  oh god
	_, err := r.tx.Exec(`INSERT INTO runs(runid, node_count, visibility_delay, query_delay,
			block_size, deadline, bandwidth, latency, duration, dup_blocks, workload) values(?,
			?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`, r.rid, config["node_count"], config["visibility_delay"],
		config["query_delay"], config["block_size"], config["deadline"], config["bandwidth"],
		config["latency"], duration, dup, workload)
	check(err)

	err = r.tx.Commit()
	check(err)

	r.ReportStats()
	r.db.Close()
}

//  Creates and starts a new timer.
//  Returns id of timer which should be given to an EndTime method to stop it and record the time.
func (r *Recorder) NewTimer() int {
	r.newTimerMutex.Lock()
	r.currID += 1
	r.newTimerMutex.Unlock()

	r.times[r.currID] = time.Now()
	return r.currID
}

//  Accepts a timer ID (given by StartFileTime) and records the elapsed time.
func (r *Recorder) EndFileTime(id int, pid string, filename string) {
	tstamp := (r.times[id].UnixNano() - r.createdAt.UnixNano()) / 1000

	elapsed := time.Since(r.times[id])
	t := elapsed.Seconds()
	delete(r.times, id)

	fileTimes.With(getLabels()).Observe(elapsed.Seconds() * 1000)

	//  how to best get file size without opening it?
	bs, err := strconv.Atoi(config["block_size"])
	check(err)
	//  multiply block size with # of blocks that make file to get file size...
	fsize := bs * len(files[filename])

	r.stmnts["file_times"].Exec(tstamp, t, r.rid, pid, fsize)
	updateDupBlocks()
}

//  Ends timer with given id and records data for peer with given pretty id
func (r *Recorder) EndBlockTime(id int, pid string) {
	tstamp := (r.times[id].UnixNano() - r.createdAt.UnixNano()) / 1000

	elapsed := time.Since(r.times[id])
	t := elapsed.Seconds() * 1000
	delete(r.times, id)

	blockTimes.With(getLabels()).Observe(elapsed.Seconds() * 1000)

	r.stmnts["block_times"].Exec(tstamp, t, r.rid, pid)
	//updateDupBlocks()
}

//  Returns mean block request fulfillment time of an instance in ms
func (r *Recorder) MeanBlockTime(inst bs.Instance) (float64, error) {
	s, err := inst.Exchange.Stat()
	if err != nil {
		return 0, fmt.Errorf("Couldn't get stats for peer %v.", inst.Peer)
	}
	if s.BlocksReceived == 0 {
		return 0, errors.New("No blocks for peer.")
	}

	sum, err := r.sumTimesForPeer(inst.Peer, "block_times")
	if err != nil {
		return 0, err
	}
	return sum / float64(s.BlocksReceived), nil
}

func (r *Recorder) ElapsedTime() string {
	return time.Since(r.createdAt).String()
}

//  Returns mean block request fulfillment time in ms across all bs instances
func (r *Recorder) TotalMeanBlockTime(insts []bs.Instance) float64 {
	var b int
	for _, inst := range insts {
		s, err := inst.Exchange.Stat()
		if err != nil {
			fmt.Println("Couldn't get stats for peer ", inst.Peer)
			continue
		}
		if s.BlocksReceived == 0 {
			continue
		}
		b += s.BlocksReceived
	}
	return r.sumTimes("block_times") / float64(b)
}

func (r *Recorder) TotalFileTime() float64 {
	return r.sumTimes("file_times")
}

//  Returns mean file request fulfillment time in ms across all bs instances
func (r *Recorder) TotalMeanFileTime() float64 {
	var n float64
	row := r.db.QueryRow("SELECT COUNT(time) FROM file_times WHERE runid=" + strconv.Itoa(r.rid))
	err := row.Scan(&n)
	check(err)
	return r.sumTimes("file_times") / float64(n)
}

//  Sums all table (block or file) times of this run
func (r *Recorder) sumTimes(table string) float64 {
	var t float64
	row := r.db.QueryRow("SELECT SUM(time) FROM " + table + " WHERE runid=" + strconv.Itoa(r.rid))
	err := row.Scan(&t)
	check(err)
	return t
}

func (r *Recorder) sumTimesForPeer(pid peer.ID, field string) (float64, error) {
	var t float64
	query := `SELECT SUM(time) FROM ` + field + ` WHERE peerid="` + pid.Pretty() + `"AND runid=` + strconv.Itoa(r.rid)
	row := r.db.QueryRow(query)
	err := row.Scan(&t)
	if err != nil {
		return 0, err
	}
	return t, nil
}

func TotalBlocksReceived(peers []bs.Instance) int {
	var blocks int
	for _, p := range peers {
		pstat, err := p.Exchange.Stat()
		if err != nil {
			fmt.Println("Unable to get stats from peer ", p.Peer)
		}
		blocks += pstat.BlocksReceived
	}
	return blocks
}

func DupBlocksReceived(peers []bs.Instance) int {
	var blocks int
	for _, p := range peers {
		pstat, err := p.Exchange.Stat()
		if err != nil {
			fmt.Println("Unable to get stats from peer ", p.Peer)
		}
		blocks += pstat.DupBlksReceived
	}
	return blocks
}

func getLabels() prom.Labels {
	if currLables != nil {
		return currLables
	}

	currLables := make(map[string]string, 0)
	for _, label := range labels {
		currLables[label] = config[label]
	}
	return currLables
}

func updateDupBlocks() {
	var blocks int
	for _, p := range peers {
		pstat, err := p.Exchange.Stat()
		if err != nil {
			fmt.Println("Unable to get stats from peer ", p.Peer)
		}
		blocks += pstat.BlocksReceived
	}
	dupBlocks.With(getLabels()).Set(float64(blocks))
}

func (r *Recorder) ReportStats() {
	fmt.Println("\n\n==============STATS=============\n\n")
	for num, peer := range peers {
		s, err := peer.Exchange.Stat()
		if err != nil {
			fmt.Println("Couldn't get stats for peer ", peer)
			continue
		}
		if s.BlocksReceived > 0 {
			mbt, err := recorder.MeanBlockTime(peer)
			if err != nil {
				fmt.Println("Error when getting mean time of peer ", peer)
				continue
			}
			fmt.Printf("Peer %d, %s: %fms mean time, %d blocks, %d duplicate blocks.\n",
				num, peer.Peer.String(), mbt, s.BlocksReceived, s.DupBlksReceived)
		}
	}
	fmt.Printf("Mean block time: %fms.\n", r.TotalMeanBlockTime(peers))
	fmt.Printf("Total blocks received: %d.\n", TotalBlocksReceived(peers))
	fmt.Printf("Duplicate blocks received: %d.\n", DupBlocksReceived(peers))
	fmt.Printf("Mean file time: %fs.\n", r.TotalMeanFileTime())
	fmt.Printf("Simulation took: %s.\n", r.ElapsedTime())
}
