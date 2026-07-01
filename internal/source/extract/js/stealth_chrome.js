if (!window.chrome) {
  window.chrome = {
    runtime: {
      onMessage: { addListener: () => {}, removeListener: () => {} },
      sendMessage: () => {},
      connect: () => ({ onMessage: { addListener: () => {} }, postMessage: () => {} })
    },
    loadTimes: () => ({}),
    csi: () => ({})
  };
}