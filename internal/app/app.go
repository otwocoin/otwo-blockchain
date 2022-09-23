package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"path"
	"strconv"
	"text/template"

	"github.com/avvvet/oxygen/pkg/blockchain"
	"github.com/avvvet/oxygen/pkg/kv"
	"github.com/avvvet/oxygen/pkg/wallet"
	"github.com/common-nighthawk/go-figure"
	pubsub "github.com/libp2p/go-libp2p-pubsub"
	"github.com/savioxavier/termlink"
	"go.uber.org/zap"
)

const tempDir = "templates"

var (
	logger, _ = zap.NewDevelopment()
)

type ReqUTXO struct {
	WalletAddress string
}

type HttpServer struct {
	port        uint
	topic       *pubsub.Topic
	chain       *blockchain.Chain
	walletStore []*wallet.WalletAddressByte
	context     context.Context
}

type RenderBlock struct {
	Timestamp   int64
	Hash        string
	Data        []byte
	Transaction []*blockchain.Transaction
	MerkleRoot  []byte
	PrevHash    string
	Nonce       int
	BlockHeight int
	Difficulty  int
}

func (h *HttpServer) GetPort() uint {
	return h.port
}

func NewApp(ctx context.Context, port uint, t *pubsub.Topic,
	wl []*wallet.WalletAddressByte, c *blockchain.Chain) *HttpServer {
	return &HttpServer{port, t, c, wl, ctx}
}

func NewDir(path string) {
	if _, err := os.Stat(path); errors.Is(err, os.ErrNotExist) {
		err := os.MkdirAll(path, os.ModePerm)
		if err != nil {
			log.Println(err)
		}
		logger.Sugar().Infof("local ledger %v created \n", path)
	} else {
		logger.Sugar().Infof("local ledger %v found \n", path)
	}
}

func InitWalletLedger(path string) ([]*wallet.WalletAddressByte, error) {
	var listWallet []*wallet.WalletAddressByte

	ledger, err := kv.NewLedger(path)
	if err != nil {
		logger.Sugar().Fatal("unable to initialize wallet ledger.")
	}

	iter := ledger.Db.NewIterator(nil, nil)
	if !iter.Last() {
		wallet := wallet.NewWallet()
		/* encode wallet before storing and decod before usage*/
		walletByte := wallet.EncodeWallet()
		b, _ := json.Marshal(walletByte)

		err = ledger.Upsert([]byte(wallet.WalletAddress), b)
		if err != nil {
			return nil, err
		}
		iter.Release()
		listWallet = append(listWallet, walletByte)
		logger.Sugar().Info("new wallet address created and stored in local ledger \n")
		return listWallet, err
	}

	for ok := iter.First(); ok; ok = iter.Next() {

		v := iter.Value()

		w := &wallet.WalletAddressByte{}
		err = json.Unmarshal(v, w)
		listWallet = append(listWallet, w)
		if err != nil {
			return nil, err
		}
	}
	iter.Release()
	logger.Sugar().Info("existing wallet fetched \n")
	return listWallet, nil
}

func (h *HttpServer) Status(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		w.Header().Add("Content-Type", "application/json")
		//lets create struct on th fly
		data, _ := json.Marshal(struct {
			Status string
		}{
			Status: "server started...",
		})
		io.WriteString(w, string(data[:]))
	}
}

func (h *HttpServer) Chains(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		w.Header().Add("Content-Type", "application/json")
		var blockRecord []RenderBlock

		var i = 0
		for {
			data, err := h.chain.Ledger.Get([]byte(strconv.Itoa(i)))
			if err != nil {
				break
			} else {
				block := &blockchain.Block{}
				err = json.Unmarshal(data, block)
				if err != nil {
					blockRecord = append(blockRecord, RenderBlock{})
					continue
				}

				rb := &RenderBlock{
					block.Timestamp,
					fmt.Sprintf("%x", block.Hash),
					block.Data,
					block.Transaction,
					block.MerkleRoot,
					fmt.Sprintf("%x", block.PrevHash),
					block.Nonce,
					block.BlockHeight,
					block.Difficulty,
				}

				blockRecord = append(blockRecord, *rb)
			}
			i++
		}

		m, _ := json.Marshal(struct {
			Blocks []RenderBlock `json:"block"`
			Length int           `json:"length"`
		}{
			Blocks: blockRecord,
			Length: len(blockRecord),
		})
		io.WriteString(w, string(m[:]))
	}
}

func (h *HttpServer) UTXO(w http.ResponseWriter, req *http.Request) {
	switch req.Method {
	case http.MethodGet:
		w.Header().Add("Content-Type", "application/json")
		body := &ReqUTXO{}
		decoder := json.NewDecoder(req.Body)
		err := decoder.Decode(body)
		if err != nil {
			io.WriteString(w, "error to decode")
			return
		}
		utxo, balance := h.chain.GetUTXO(body.WalletAddress)

		m, _ := json.Marshal(struct {
			UTXO    []blockchain.UTXO `json:"utxo"`
			Balance int               `json:"unspent_balance"`
		}{
			UTXO:    utxo,
			Balance: balance,
		})
		io.WriteString(w, string(m[:]))
	}
}

func (h *HttpServer) Index(w http.ResponseWriter, req *http.Request) {
	switch req.Method {
	case http.MethodGet:
		t, _ := template.ParseFiles(path.Join(tempDir, "index.html"))
		t.Execute(w, "")
	default:
		log.Printf("ERROR: Invalid HTTP Method")
	}
}

func (h *HttpServer) Run() {
	http.HandleFunc("/", h.Status)
	http.HandleFunc("/chains", h.Chains)
	http.HandleFunc("/utxo", h.UTXO)

	listener, err := net.Listen("tcp", ":"+strconv.Itoa(int(h.port)))
	if err != nil {
		panic(err)
	}
	logger.Sugar().Infof("listening web requests at port 😎️ %v", listener.Addr().(*net.TCPAddr).Port)
	fmt.Println("")
	fmt.Println(termlink.ColorLink("access your otwo blockchain node at this link 👉️ ", "http://127.0.0.1:"+strconv.Itoa(listener.Addr().(*net.TCPAddr).Port), "italic yellow"))
	if err := http.Serve(listener, nil); err != nil {
		logger.Sugar().Fatal(err)
	}
}

func Art() {
	fmt.Println()
	fmt.Println("blockchain node and secure wallet")
	myFigure := figure.NewColorFigure("otwo blockchain", "", "yellow", true)
	myFigure.Print()
	fmt.Println()
}
