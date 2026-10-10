import { ipcRenderer } from "electron";
ipcRenderer.once("deeper-recorder-port", event => {
  window.postMessage("deeper-recorder-port", "*", event.ports);
});
