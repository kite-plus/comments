import { render } from "preact";
import { App, type Data } from "./App";
import "./styles.css";

const el = document.getElementById("kc-data");
const data = JSON.parse(el?.textContent ?? "{}") as Data;
render(<App d={data} />, document.getElementById("kc-root")!);
