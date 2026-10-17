import { render } from "preact";
import { App, type Data } from "./App";
import { storage } from "./api";
import "./styles.css";

// Left by the guest form, which is gone: nickname, email, homepage, token.
storage.del("kc:g");
storage.del("kc:gt");

const el = document.getElementById("kc-data");
const data = JSON.parse(el?.textContent ?? "{}") as Data;
render(<App d={data} />, document.getElementById("kc-root")!);
