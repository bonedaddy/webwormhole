package main

//rollback test
// http://wpt.live/webrtc/RTCPeerConnection-setLocalDescription-rollback.html
//
// https://w3c.github.io/webrtc-pc/#rtcsignalingstate-enum
//
// https://webrtc.github.io/adapter/adapter-latest.js

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"flag"
	"log"
	"net/http"
	"os"
	"sync"

	"golang.org/x/crypto/acme/autocert"
)

type sessiondesc struct {
	Type string `json:"type"`
	SDP  string `json:"sdp"`
}

type session struct {
	offer  *sessiondesc
	answer *sessiondesc
	c      *sync.Cond
}

var slots = struct {
	m map[string]*session
	sync.RWMutex
}{m: make(map[string]*session)}

func serveHTTP(w http.ResponseWriter, r *http.Request) {
	slotkey := r.URL.Path

	if r.Method == http.MethodGet && slotkey == "/" {
		w.Write([]byte(indexpage))
		return
	}

	w.Header().Set("Access-Control-Allow-Origin", "*")
	if r.Method != http.MethodPost {
		http.Error(w, "invalid method", 400)
	}

	enc := json.NewEncoder(w)
	dec := json.NewDecoder(r.Body)
	var msg sessiondesc
	err := dec.Decode(&msg)
	if err != nil {
		log.Printf("%v", err)
		return
	}

	log.Printf("%v: post", slotkey)

	slots.Lock()
	slot := slots.m[slotkey]
	if slot == nil {
		if msg.Type != "offer" {
			slots.Unlock()
			log.Printf("%v: [1] not an offer", slotkey)
			http.Error(w, "invalid offer description", 400)
			return
		}

		// New offer (probably)
		slot = &session{
			offer: &msg,
			c:     sync.NewCond(&sync.Mutex{}),
		}
		slot.c.L.Lock()
		slots.m[slotkey] = slot
		slots.Unlock()

		for slot.answer == nil {
			slot.c.Wait()
		}

		err := enc.Encode(slot.answer)
		slot.c.L.Unlock()
		if err != nil {
			log.Printf("%v", err)
			return
		}

		slots.Lock()
		delete(slots.m, slotkey)
		slots.Unlock()
	} else {
		slots.Unlock()
		if msg.Type == "offer" {
			// Already have offer, pass that down
			err := enc.Encode(slot.offer)
			if err != nil {
				log.Printf("%v", err)
				return
			}
		} else if msg.Type == "answer" {
			// This is an answer to an offer, wake the other go routines up.
			slot.answer = &msg
			slot.c.Broadcast()
		}
	}
}

func main() {
	httpaddr := flag.String("http", ":http", "http listen address")
	httpsaddr := flag.String("https", ":https", "https listen address")
	secretpath := flag.String("secrets", os.Getenv("HOME")+"/keys", "path to put let's encrypt cache")
	flag.Parse()

	m := &autocert.Manager{
		Cache:  autocert.DirCache(*secretpath),
		Prompt: autocert.AcceptTOS,
		HostPolicy: func(ctx context.Context, host string) error {
			if host == "minimumsignal.0f.io" {
				return nil
			}
			return errors.New("request host does not point to allowed cname")
		},
	}

	srv := &http.Server{
		Addr:    *httpaddr,
		Handler: m.HTTPHandler(http.HandlerFunc(serveHTTP)),
	}
	go func() { log.Fatal(srv.ListenAndServe()) }()

	ssrv := &http.Server{
		Addr:      *httpsaddr,
		Handler:   http.HandlerFunc(serveHTTP),
		TLSConfig: &tls.Config{GetCertificate: m.GetCertificate},
	}
	log.Fatal(ssrv.ListenAndServeTLS("", ""))
}

var indexpage=`
<!DOCTYPE html>
<meta charset=utf-8>
<link rel="canonical" href="https://minimimsignal.0f.io/">
<title>minimum signal</title>
<style>
body {
  font: small arial, sans-serif;
  max-width: 32em;
  margin: auto;
  padding: 2em;
  background: #fff;
  color: #000;
}
pre {
  font: small Inconsolata, monospace;
  word-spacing: 0;
  letter-spacing: 0;
}
a {
  text-decoration: none;
}
h1, h2, h3, h4, caption, thead th {
  font-weight: normal;
  font-variant: small-caps;
  text-shadow: 0 0 1px #667;
}
h1 {
  font-size: 1.7em;
  text-align: center;
  width: 100%;
}
h2 {
  font-size:1.6em;
}
h3 {
  font-size: 1.1em;
}
footer {
  font-size: x-small;
  text-align: center;
}
</style>
<body>

<h1>MINIMUM SIGNAL</h1>

<p>Experimental service to handle <a href="https://developer.mozilla.org/en-US/docs/Web/API/WebRTC_API">WebRTC</a> singalling so you don't have to.</p>

<h2>RATIONALE</h2>

<p>While WebRTC's main selling point is that it is peer-to-peer, every WebRTC application needs a central signalling server to facilitate establishing this direct connection.</p>

<p>Writing these is somewhat tedious and requires setting up the infrastructure to host it. What if this existed as a service that you could just use and focus on building the client side of your WebRTC application?</p>

<p>This way, no special server-side code needs to be written. The client parts (HTML/JS/CSS) could be hosted on some static service like S3 or GitHub Pages, or they could be native applications.</p>

<h2>MODEL</h2>

<p>WebRTC uses an "offer" and "answer" model, where one party puts sends an "offer" encoded in a JSON object and the other party responds similarly with an "answer" JSON object. Minimum signal uses a slot system to allow clients to exchange offers and answers.</p>

<p>Slots are arbitrary strings, currently capped at 255 bytes. If Alice wants to reach Bob, then they or their user agents perform the following steps:</p>

<ol>
<li>A uploads its offer object to Minimum Signal at some arbitrary slot.</li>
<li>A communicates the slot name to B out of band. E.g. message, AirDrop, email, or shout it out across the room.</li>
<li>B fetches A's offer and uploads its own.
<li>A receives B's offer and they both carry on the WebRTC nogotiations directly.
</ol>

<p>At this point, Minimum Signal's role is finished and the slot is free to be used by someone else. This slot model is similar to what the non-crypto parts of <a href="https://github.com/warner/magic-wormhole">Magic Wormhole</a> use.</p>

<h2>API</h2>

<p>There is only one endpoint supported:</p>
<pre>https://minimumsignal.0f.io/$slot</pre>
<p>where $slot is the slot name.</p>
<p>There is only one method supported, POST with the SDP as body.</p>
<p>If the SDP is of type "offer" and the slot is free, the request will block until someone uploads an answer to the same slot, at which point it will return the answer.
<p>If the SDP is of type "offer" and the slot is busy, the response will be the original offer.
<p>If the SDP is of type "answer", it will be forwarded to the original sender of the offer (who up until this point has been blocked).
<p>All other requests are invalid.</p>

<p>The intended usage is that both parties, A and B, race to upload their offers to the same slot. Whichever of them loses has to accept the other one's offer and upload an answer based on it.

<h2>SECURITY CONSIDIRATIONS</h2>

<p>On its own, this scheme is not secure.</p>

<p>In the best case, assuming the slot name is a long and difficult to guess string, the trust model would still have to include the operator of the signalling server, since they can see and potentially modify both parties' SDPs.</p>

<p>For a demo that might be good enough, but for any useful application you'll need to implement a way for A to authenticate B on this potentially untrusted link. Some PAKE might be a good way to do it and fits well with the slot system. Again, cf. Magic Wormhole.</p>

<h2>USAGE EXAMPLE</h2>

<p>Here's some example JavaScript to demostrate the usage of the API. The dial() function returns an RTCPeerConnection object.</p>

<pre>
// initconn initialises a peer connection by adding streams or data channels.
// Modify as needed.
let initconn = pc => {
}

let dial = async (slot, config) => {
	let pc = new RTCPeerConnection(config);

	initconn(pc);

	// Create an offer.
	await pc.setLocalDescription(await pc.createOffer())

	// Wait for ICE candidates.
	await new Promise(r=>{pc.onicecandidate=e=>{if(e.candidate === null){r()}}})

	// Upload offer.
	let response = await fetch(`+"`https://minimumsignal.0f.io/${slot}`"+`, {
		method: 'POST',
		body: JSON.stringify(pc.localDescription)
	})
	let remote = await response.json();

	if (remote["type"] === "offer") {
		// We got back another offer, which means someone else (possibly
		// the party we're trying to reach) beat us to this slot.

		// Throw away our offer and accept this one, creating an answer.
		pc = new RTCPeerConnection(config);
		initconn(pc);
		// await pc.setLocalDescription({"type":"rollback"});
		await pc.setRemoteDescription(new RTCSessionDescription(remote));
		await pc.setLocalDescription(await pc.createAnswer());

		// Wait for ICE candidates.
		await new Promise(r=>{pc.onicecandidate=e=>{if(e.candidate === null){r()}}})

		// Upload answer.
		await fetch(`+"`https://minimumsignal.0f.io/${slot}`"+`, {
			method: 'POST',
			body: JSON.stringify(pc.localDescription)
		})
	} else if (remote["type"] === "answer") {
		// We got back an answer to our offer. Accept it.
		await pc.setRemoteDescription(new RTCSessionDescription(remote));
	}

	// We're done.
	return pc
}
</pre>

<h2>DISCLAIMER</h2>

<p>The authors takes absolutely no responsibity and offers no promises for the reliability or availability of this experiment.</p>

<p>We reserve the right to call it quits any time. If Google can do this we sure can.</p>

<footer>
Comments &amp; complaints <a href="https://0x65.net" rel="author">salman aljammaz</a>: <a href="https://twitter.com/_saljam">@_saljam</a> or <a href="mailto:s@aljmz.com">s@aljmz.com</a>
</footer>
`