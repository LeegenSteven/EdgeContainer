// Integrate in the real browser client owning the same /v2v/stream WebSocket.
// Call finalReply only when the LLM reply is complete, BEFORE tts_flush.
// Neither this helper nor the adapter captures audio, images, or LLM credentials.
export function businessEventHooks(socket) {
  function send(payload) {
    if (socket.readyState === WebSocket.OPEN) socket.send(JSON.stringify(payload));
  }
  return {
    finalReply(text) {
      send({type: "edgestack_assistant_final", text});
    },
    playing() {
      send({type: "edgestack_playback_ack", phase: "playing"});
    },
    played() {
      // Invoke after the last scheduled AudioBufferSourceNode.onended or the
      // HTMLAudioElement 'ended' event for this reply, not upon server tts_done.
      send({type: "edgestack_playback_ack", phase: "played"});
    },
    interrupted() {
      // Invoke only after the client has stopped its playback queue.
      send({type: "edgestack_playback_ack", phase: "interrupted"});
    },
  };
}
