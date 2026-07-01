if (window.RTCPeerConnection) {
  const OrigRTC = window.RTCPeerConnection;
  window.RTCPeerConnection = function(config, constraints) {
    if (config && config.iceServers) { config.iceServers = []; }
    return new OrigRTC(config, constraints);
  };
  window.RTCPeerConnection.prototype = OrigRTC.prototype;
  Object.defineProperty(window.RTCPeerConnection, 'name', { value: 'RTCPeerConnection' });
}