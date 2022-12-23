package main

import (
	"encoding/json"
	"fmt"
	"github.com/filecoin-project/go-address"
	"github.com/filecoin-project/index-provider/metadata"
	cliutil "github.com/filecoin-project/lotus/cli/util"
	"github.com/ipfs/go-cid"
	"github.com/libp2p/go-libp2p/core/peer"
	"html/template"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"sync"

	"github.com/gorilla/mux"
	"github.com/ipld/go-ipld-prime/node/basicnode"
	"github.com/ipld/go-ipld-prime/traversal/selector"
	"github.com/ipld/go-ipld-prime/traversal/selector/builder"
	"golang.org/x/xerrors"

	"github.com/filecoin-project/go-fil-markets/storagemarket"
	"github.com/filecoin-project/go-state-types/abi"
	"github.com/filecoin-project/go-state-types/builtin/v8/market"

	"github.com/filecoin-project/lotus/chain/types"
)

func (h *dxhnd) handleMiners(w http.ResponseWriter, r *http.Request) {
	tpl, err := template.ParseFS(dres, "dexpl/miners.gohtml")
	if err != nil {
		fmt.Println(err)
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html")
	w.WriteHeader(http.StatusOK)
	data := map[string]interface{}{
		"miners": h.mminers,
	}
	if err := tpl.Execute(w, data); err != nil {
		fmt.Println(err)
		return
	}
}

func (h *dxhnd) handleDeals(w http.ResponseWriter, r *http.Request) {
	deals, err := h.api.ClientListDeals(r.Context())
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	tpl, err := template.ParseFS(dres, "dexpl/client_deals.gohtml")
	if err != nil {
		fmt.Println(err)
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html")
	w.WriteHeader(http.StatusOK)
	data := map[string]interface{}{
		"deals":             deals,
		"StorageDealActive": storagemarket.StorageDealActive,
	}
	if err := tpl.Execute(w, data); err != nil {
		fmt.Println(err)
		return
	}
}

func (h *dxhnd) handleClients(w http.ResponseWriter, r *http.Request) {

	type clEntry struct {
		Addr  address.Address
		Count int64
		Data  string
	}

	var clEnts []clEntry
	cf, err := os.OpenFile(filepath.Join(h.clientMeta, "clients.json"), os.O_RDONLY, 0666)
	if err != nil {
		fmt.Println(err)
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if err := json.NewDecoder(cf).Decode(&clEnts); err != nil {
		fmt.Println(err)
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	cf.Close()

	tpl, err := template.ParseFS(dres, "dexpl/clients.gohtml")
	if err != nil {
		fmt.Println(err)
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html")
	w.WriteHeader(http.StatusOK)

	data := map[string]interface{}{
		"clients": clEnts,
	}
	if err := tpl.Execute(w, data); err != nil {
		fmt.Println(err)
		return
	}
}

func (h *dxhnd) handleClient(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	ma, err := address.NewFromString(vars["id"])
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	type provMeta struct {
		Prov address.Address
		Data string
		Deal abi.DealID
	}

	clDeals := make([]provMeta, 0)

	cf, err := os.OpenFile(filepath.Join(h.clientMeta, fmt.Sprintf("cl-%s.json", ma)), os.O_RDONLY, 0666)
	if err != nil {
		fmt.Println(err)
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if err := json.NewDecoder(cf).Decode(&clDeals); err != nil {
		fmt.Println(err)
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	cf.Close()

	tpl, err := template.ParseFS(dres, "dexpl/client.gohtml")
	if err != nil {
		fmt.Println(err)
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html")
	w.WriteHeader(http.StatusOK)

	data := map[string]interface{}{
		"deals": clDeals,
	}
	if err := tpl.Execute(w, data); err != nil {
		fmt.Println(err)
		return
	}
}

type DealInfo struct {
	DealCID cid.Cid
	Client  address.Address
	Filplus bool
	Size    string
}

func (h *dxhnd) handleMinerSectors(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	vars := mux.Vars(r)
	ma, err := address.NewFromString(vars["id"])
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	ms, err := h.api.StateMinerSectors(ctx, ma, nil, types.EmptyTSK)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	head, err := h.api.ChainHead(ctx)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	mp, err := h.api.StateMinerPower(ctx, ma, head.Key())
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	var deals []abi.DealID
	for _, info := range ms {
		for _, d := range info.DealIDs {
			deals = append(deals, d)
		}
	}

	commps := map[abi.DealID]DealInfo{}
	var wg sync.WaitGroup
	wg.Add(len(deals))
	var lk sync.Mutex

	for _, deal := range deals {
		go func(deal abi.DealID) {
			defer wg.Done()

			md, err := h.api.StateMarketStorageDeal(ctx, deal, types.EmptyTSK)
			if err != nil {
				return
			}

			if md.Proposal.Provider == address.Undef {
				return
			}

			lk.Lock()
			commps[deal] = DealInfo{
				DealCID: md.Proposal.PieceCID,
				Client:  md.Proposal.Client,
				Filplus: md.Proposal.VerifiedDeal,
				Size:    types.SizeStr(types.NewInt(uint64(md.Proposal.PieceSize))),
			}

			lk.Unlock()
		}(deal)
	}
	wg.Wait()

	// filter out inactive deals
	for _, m := range ms {
		filtered := make([]abi.DealID, 0, len(m.DealIDs))
		for _, d := range m.DealIDs {
			if _, found := commps[d]; found {
				filtered = append(filtered, d)
			}
		}
		m.DealIDs = filtered
	}

	now, err := h.api.ChainHead(r.Context())
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	tpl, err := template.New("sectors.gohtml").Funcs(map[string]interface{}{
		"EpochTime": func(e abi.ChainEpoch) string {
			return cliutil.EpochTime(now.Height(), e)
		},
	}).ParseFS(dres, "dexpl/sectors.gohtml")
	if err != nil {
		fmt.Println(err)
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html")
	w.WriteHeader(http.StatusOK)
	data := map[string]interface{}{
		"maddr":   ma,
		"sectors": ms,
		"deals":   commps,

		"qap": types.SizeStr(mp.MinerPower.QualityAdjPower),
		"raw": types.SizeStr(mp.MinerPower.RawBytePower),
	}
	if err := tpl.Execute(w, data); err != nil {
		fmt.Println(err)
		return
	}
}

func (h *dxhnd) handleDeal(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	did, err := strconv.ParseInt(vars["id"], 10, 64)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	ctx := r.Context()

	d, err := h.api.StateMarketStorageDeal(ctx, abi.DealID(did), types.EmptyTSK)
	if err != nil {
		http.Error(w, xerrors.Errorf("StateMarketStorageDeal: %w", err).Error(), http.StatusInternalServerError)
		return
	}

	lstr, err := d.Proposal.Label.ToString()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	dcid, err := cid.Parse(lstr)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	lstr = dcid.String()
	d.Proposal.Label, _ = market.NewLabelFromString(lstr) // if it's b64, will break urls

	var cdesc string

	{
		// get left side of the dag up to typeCheckDepth
		g := getFilRetrieval(h.tempBsBld, h.apiBss, h.api, r, d.Proposal.Provider, d.Proposal.PieceCID, dcid)

		ssb := builder.NewSelectorSpecBuilder(basicnode.Prototype.Any)
		root, dserv, _, done, err := g(ssb.ExploreRecursive(selector.RecursionLimitDepth(typeCheckDepth),
			ssb.ExploreUnion(ssb.Matcher(), ssb.ExploreFields(func(eb builder.ExploreFieldsSpecBuilder) {
				eb.Insert("Links", ssb.ExploreIndex(0, ssb.ExploreFields(func(eb builder.ExploreFieldsSpecBuilder) {
					eb.Insert("Hash", ssb.ExploreRecursiveEdge())
				})))
			})),
		))
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		defer done()

		// this gets type / size / linkcount for root

		desc, _, err := linkDesc(ctx, root, "", dserv)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		cdesc = desc.Desc

		if desc.Size != "" {
			cdesc = fmt.Sprintf("%s %s", cdesc, desc.Size)
		}
	}

	now, err := h.api.ChainHead(r.Context())
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	resp, err := h.idx.Find(ctx, dcid.Hash())
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	bitswapProvs := map[peer.ID]int{}
	filProvs := map[peer.ID]int{}

	for _, result := range resp.MultihashResults {
		for _, providerResult := range result.ProviderResults {
			var meta metadata.Metadata
			if err := meta.UnmarshalBinary(providerResult.Metadata); err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}

			for _, proto := range meta.Protocols() {
				switch meta.Get(proto).(type) {
				case *metadata.GraphsyncFilecoinV1:
					filProvs[providerResult.Provider.ID]++
				case *metadata.Bitswap:
					bitswapProvs[providerResult.Provider.ID]++
				}
			}
		}
	}

	tpl, err := template.New("deal.gohtml").Funcs(map[string]interface{}{
		"EpochTime": func(e abi.ChainEpoch) string {
			return cliutil.EpochTime(now.Height(), e)
		},
		"SizeStr": func(s abi.PaddedPieceSize) string {
			return types.SizeStr(types.NewInt(uint64(s)))
		},
	}).ParseFS(dres, "dexpl/deal.gohtml")
	if err != nil {
		fmt.Println(err)
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html")
	w.WriteHeader(http.StatusOK)
	data := map[string]interface{}{
		"deal":  d,
		"label": lstr,
		"id":    did,

		"provsBitswap": len(bitswapProvs),
		"provsFil":     len(filProvs),

		"contentDesc": cdesc,
	}
	if err := tpl.Execute(w, data); err != nil {
		fmt.Println(err)
		return
	}
}
