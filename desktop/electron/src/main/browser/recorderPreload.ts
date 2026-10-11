import { ipcRenderer } from "electron";
ipcRenderer.once("deepzora-recorder-port", event => {
  window.postMessage("deepzora-recorder-port", "*", event.ports);
});
