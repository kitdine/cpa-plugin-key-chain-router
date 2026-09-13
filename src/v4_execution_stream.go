package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/http"
	"net/url"
	"time"
)

func runStreamPolicyV4(trace string, p *Policy, r *PolicyRule, ranked []*PolicyCandidate, source, clientModel string, body []byte, headers http.Header, query url.Values, alt, callbackID, outStreamID string, started time.Time) {
	event := RoutingEvent{TraceID: trace, At: nowV4(), Decision: decisionHandled, PolicyName: p.Name, KeyFingerprint: p.KeyFingerprint, KeyHint: p.KeyHint, RuleID: r.ID, RuleName: r.Name, Strategy: r.Strategy, Model: clientModel, Stream: true, ruleSnapshot: cloneRuleV4(r)}
	attempted := map[string]bool{}
	currentPriority := math.MaxInt
	nextAction := failNext
	max := r.Failover.MaxAttempts
	if max <= 0 { max = len(ranked) }
	var lastErr error
	var activeCandidate *PolicyCandidate
	activeProbe := false
	defer func() {
		if x := recover(); x != nil {
			releaseCandidateProbeV4(p, r, activeCandidate, activeProbe)
			_ = closeOutputStream(outStreamID, fmt.Sprintf("panic: %v", x))
		}
	}()

	for len(event.Attempts) < max {
		c, probe, healthSkips := nextHealthyCandidateWithSkipsV4(p, r, ranked, attempted, nextAction, currentPriority)
		nextAction = failNext
		if c == nil { break }
		event.healthSkips = append(event.healthSkips, healthSkips)
		activeCandidate, activeProbe = c, probe
		attempted[c.ID] = true
		currentPriority = c.Priority

		resp, ar, err := executeCandidateV4(c, source, clientModel, body, headers, query, alt, callbackID, true)
		if err != nil {
			event.Attempts = append(event.Attempts, ar)
			lastErr = err
			recordCandidateExecutionFailureV8(p, r, c, ar.Status, err, nil, probe)
			clearProbeOwnershipV4(&activeProbe)
			action := failureActionV4(r.Failover, ar.Status, err)
			if action == failCPADefault { runCPADefaultStreamV4(event, source, clientModel, body, headers, query, alt, callbackID, outStreamID, started); return }
			if action == failStop { break }
			nextAction = action
			continue
		}

		var sr hostModelStreamResponse
		if err = json.Unmarshal(resp.Body, &sr); err != nil {
			ar.Error = err.Error()
			event.Attempts = append(event.Attempts, ar)
			lastErr = err
			recordCandidateExecutionFailureV8(p, r, c, 0, err, nil, probe)
			clearProbeOwnershipV4(&activeProbe)
			action := failureActionV4(r.Failover, 0, err)
			if action == failCPADefault { runCPADefaultStreamV4(event, source, clientModel, body, headers, query, alt, callbackID, outStreamID, started); return }
			if action == failStop { break }
			nextAction = action
			continue
		}
		ar.Status = sr.StatusCode
		event.Attempts = append(event.Attempts, ar)
		if sr.StatusCode < 200 || sr.StatusCode >= 400 {
			if sr.StreamID != "" { _ = closeHostStream(sr.StreamID) }
			lastErr = fmt.Errorf("upstream status %d", sr.StatusCode)
			event.Attempts[len(event.Attempts)-1].Error = lastErr.Error()
			recordCandidateExecutionFailureV8(p, r, c, sr.StatusCode, nil, sr.Headers, probe)
			clearProbeOwnershipV4(&activeProbe)
			action := failureActionV4(r.Failover, sr.StatusCode, nil)
			if action == failCPADefault { runCPADefaultStreamV4(event, source, clientModel, body, headers, query, alt, callbackID, outStreamID, started); return }
			if action == failStop { break }
			nextAction = action
			continue
		}
		if sr.StreamID == "" {
			lastErr = errors.New("host stream id 为空")
			event.Attempts[len(event.Attempts)-1].Error = lastErr.Error()
			recordCandidateExecutionFailureV8(p, r, c, 0, lastErr, sr.Headers, probe)
			clearProbeOwnershipV4(&activeProbe)
			action := failureActionV4(r.Failover, 0, lastErr)
			if action == failCPADefault { runCPADefaultStreamV4(event, source, clientModel, body, headers, query, alt, callbackID, outStreamID, started); return }
			if action == failStop { break }
			nextAction = action
			continue
		}

		first := true
		stopAfterFirstReadFailure := false
		for {
			rr, readErr := readHostStream(sr.StreamID)
			if readErr != nil {
				_ = closeHostStream(sr.StreamID)
				lastErr = readErr
				status := statusFromError(readErr)
				recordCandidateExecutionFailureV8(p, r, c, status, readErr, sr.Headers, probe)
				clearProbeOwnershipV4(&activeProbe)
				if first {
					event.Attempts[len(event.Attempts)-1].Error = readErr.Error()
					event.Attempts[len(event.Attempts)-1].Status = status
					action := failureActionV4(r.Failover, status, readErr)
					if action == failCPADefault { runCPADefaultStreamV4(event, source, clientModel, body, headers, query, alt, callbackID, outStreamID, started); return }
					if action == failStop { stopAfterFirstReadFailure = true } else { nextAction = action }
					break
				}
				event.Success, event.Error = false, readErr.Error()
				event.DurationMs = time.Since(started).Milliseconds()
				observeV4(event, callbackID)
				_ = closeOutputStream(outStreamID, readErr.Error())
				return
			}
			if rr.Error != "" {
				_ = closeHostStream(sr.StreamID)
				lastErr = errors.New(rr.Error)
				status := statusFromError(lastErr)
				recordCandidateExecutionFailureV8(p, r, c, status, lastErr, sr.Headers, probe)
				clearProbeOwnershipV4(&activeProbe)
				if first {
					event.Attempts[len(event.Attempts)-1].Error = rr.Error
					event.Attempts[len(event.Attempts)-1].Status = status
					action := failureActionV4(r.Failover, status, lastErr)
					if action == failCPADefault { runCPADefaultStreamV4(event, source, clientModel, body, headers, query, alt, callbackID, outStreamID, started); return }
					if action == failStop { stopAfterFirstReadFailure = true } else { nextAction = action }
					break
				}
				event.Success, event.Error = false, rr.Error
				event.DurationMs = time.Since(started).Milliseconds()
				observeV4(event, callbackID)
				_ = closeOutputStream(outStreamID, rr.Error)
				return
			}
			if len(rr.Payload) > 0 {
				first = false
				if emitErr := emitOutput(outStreamID, rr.Payload); emitErr != nil {
					_ = closeHostStream(sr.StreamID)
					releaseCandidateProbeV4(p, r, c, probe)
					clearProbeOwnershipV4(&activeProbe)
					_ = closeOutputStream(outStreamID, emitErr.Error())
					return
				}
			}
			if rr.Done {
				_ = closeHostStream(sr.StreamID)
				recordCandidateSuccessV4(p, r, c, probe)
				clearProbeOwnershipV4(&activeProbe)
				event.Final, event.Provider, event.AuthIndex = c.Name, c.Provider, c.AuthIndex
				event.Status, event.Success = sr.StatusCode, true
				event.DurationMs = time.Since(started).Milliseconds()
				observeV4(event, callbackID)
				_ = closeOutputStream(outStreamID, "")
				return
			}
		}
		if stopAfterFirstReadFailure { break }
	}

	if r.Failover.Exhausted == failCPADefault {
		runCPADefaultStreamV4(event, source, clientModel, body, headers, query, alt, callbackID, outStreamID, started)
		return
	}
	if lastErr == nil { lastErr = errors.New("没有可用候选") }
	event.Success = false
	event.Reason, event.Error = "candidates_exhausted", lastErr.Error()
	event.DurationMs = time.Since(started).Milliseconds()
	observeV4(event, callbackID)
	_ = closeOutputStream(outStreamID, lastErr.Error())
}

func runCPADefaultStreamV4(event RoutingEvent, source, clientModel string, body []byte, headers http.Header, query url.Values, alt, callbackID, outStreamID string, started time.Time) {
	h := cloneHeader(headers)
	h.Del(ticketHeader)
	t := time.Now()
	raw, err := callHost(methodHostModelExecuteStream, map[string]any{"entry_protocol": source, "exit_protocol": source, "model": clientModel, "stream": true, "body": rewriteBodyModel(body, clientModel), "headers": h, "query": query, "alt": alt, "host_callback_id": callbackID})
	ar := attemptResult{Candidate: "CPA Default", Model: clientModel, Duration: time.Since(t)}
	ar.DurationMs = ar.Duration.Milliseconds()
	event.Decision, event.Reason = decisionFallbackToCPA, "policy_fallback_to_cpa"
	event.Attempts = append(event.Attempts, ar)
	if err != nil {
		event.Attempts[len(event.Attempts)-1].Error = err.Error()
		event.Attempts[len(event.Attempts)-1].Status = statusFromError(err)
		event.Error = err.Error()
		event.DurationMs = time.Since(started).Milliseconds()
		observeV4(event, callbackID)
		_ = closeOutputStream(outStreamID, err.Error())
		return
	}
	var sr hostModelStreamResponse
	if err = json.Unmarshal(raw, &sr); err != nil || sr.StreamID == "" {
		if err == nil { err = errors.New("host stream id 为空") }
		event.Error = err.Error()
		event.DurationMs = time.Since(started).Milliseconds()
		observeV4(event, callbackID)
		_ = closeOutputStream(outStreamID, err.Error())
		return
	}
	event.Attempts[len(event.Attempts)-1].Status = sr.StatusCode
	for {
		rr, readErr := readHostStream(sr.StreamID)
		if readErr != nil {
			_ = closeHostStream(sr.StreamID)
			event.Error = readErr.Error()
			event.DurationMs = time.Since(started).Milliseconds()
			observeV4(event, callbackID)
			_ = closeOutputStream(outStreamID, readErr.Error())
			return
		}
		if rr.Error != "" {
			_ = closeHostStream(sr.StreamID)
			event.Error = rr.Error
			event.DurationMs = time.Since(started).Milliseconds()
			observeV4(event, callbackID)
			_ = closeOutputStream(outStreamID, rr.Error)
			return
		}
		if len(rr.Payload) > 0 {
			if emitErr := emitOutput(outStreamID, rr.Payload); emitErr != nil { _ = closeHostStream(sr.StreamID); return }
		}
		if rr.Done {
			_ = closeHostStream(sr.StreamID)
			event.Final, event.Status, event.Success = "CPA Default", sr.StatusCode, true
			event.DurationMs = time.Since(started).Milliseconds()
			observeV4(event, callbackID)
			_ = closeOutputStream(outStreamID, "")
			return
		}
	}
}
