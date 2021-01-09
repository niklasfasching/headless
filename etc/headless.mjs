class CDP {
  id = 0
  commands = {}
  handlers = {}

  constructor(url) {
    this.ws = new WebSocket(url);
    this.ws.onmessage = ({data}) => {
      const json = JSON.parse(data)
      if (this.commands[json.id]) this.commands[json.id](json);
      else this.handlers[json.method]?.(json.params);
    };
    this.ws.onerror = (err) => { throw err };
    this.ready = new Promise((resolve) => { this.ws.onopen = resolve });
  }

  call(method, params) {
    const id = this.id++, err = new Error();
    return new Promise((resolve, reject) => {
      this.commands[id] = ({result, error}) => {
        delete this.commands[id];
        if (error) reject(Object.assign(err, {message: `${method}(${JSON.stringify(params)}): ${error.message} (${error.code})`}));
        else resolve(result);
      };
      this.ws.send(JSON.stringify({method, id, params}));
    });
  }

  async waitFor(expression, afterExpression = x => x) {
    const {result, exceptionDetails} = await this.call("Runtime.evaluate", {
      awaitPromise: true,
      expression: `
           (async () => {
             while (true) {
               const result = await ${expression};
               if (result) return await (${afterExpression})(result);
               await new Promise(r => setTimeout(r, 100));
             }
           })()`,
    });
    if (exceptionDetails) throw new Error(formatExceptionDetails(exceptionDetails));
    return result;
  }

  on(event, f) {
    this.handlers[event] = f;
  }
}

class Headless {
  targets = []

  constructor(url) {
    this.server = new WebSocket(url);
    this.server.call = (msg) => this.server.send(JSON.stringify(msg));
    this.server.onopen = () => this.server.call({method: "connect"});
    this.server.onerror = (err) => { throw err };
    this.server.onmessage = ({data}) => {
      const params = JSON.parse(data);
      this["on" + params.method](params);
    };
    this.ready = new Promise((resolve) => { this.resolve = resolve; });
    window.onunload = () => {
      for (const t of this.targets) this.browser.call("Target.closeTarget", t);
    };
  }

  async onconnect({browserWebsocketUrl}) {
    this.browser = new CDP(browserWebsocketUrl);
    await this.browser.ready.then(this.resolve);
    const {targetInfos} = await this.browser.call("Target.getTargets");
    const {targetId} = targetInfos.find(t => t.url === location.href);
    this.targetId = targetId;
    this.page = new CDP(`${new URL(this.browser.ws.url).origin}/devtools/page/${targetId}`);
    await this.page.ready;
    await this.page.call("Runtime.enable");
    this.page.on("Runtime.consoleAPICalled", ({type: method, args}) => {
      this.server.call({url: location.href, method, args: args.map(formatConsoleArg)});
    });
    this.page.on("Runtime.exceptionThrown", ({exceptionDetails}) => {
      this.server.call({url: location.href, method: "exception", args: [formatExceptionDetails(exceptionDetails)]});
    });
    document.body.append(document.head.querySelector("template").content);
  }

  async onopen({url}) {
    await this.ready;
    const {targetId} = await this.browser.call("Target.createTarget", {url});
    this.targets.push({targetId, url});
  }

  async onclose() {
    await this.server.call({url: location.href, method: "close"});
    await this.browser.call("Target.closeTarget", {targetId: this.targetId});
  }

  async open({url}) {
    await this.ready;
    const {targetId} = await this.browser.call("Target.createTarget", {url: "about:blank"});
    this.targets.push({targetId, url});
    const page = new CDP(`${new URL(this.browser.ws.url).origin}/devtools/page/${targetId}`);
    await page.ready;
    await page.call("Page.navigate", {url});
    return page;
  }
}

function formatConsoleArg({type: t, subtype, value, description, preview}) {
  if (["string", "number", "boolean", "undefined"].includes(t) || subtype === "null") {
    return value;
  } else if (t === "function" || subtype === "regexp") {
    return description;
  } else if (subtype === "array") {
    return `[${preview.properties.map(p => p.value).join(", ")}]`;
  } else {
    return `{${preview.properties.map(p => `${p.name}: ${p.value}`).join(", ")}}`;
  }
}

function formatExceptionDetails(exceptionDetails) {
  const {exception: {description}, url, lineNumber, columnNumber} = exceptionDetails;
  return `${description}\n    at ${url}:${lineNumber}:${columnNumber}`
}

window.headless = new Headless(`ws://${location.host}${location.pathname}`);
window.isHeadless = navigator.webdriver;
window.close = (code = 0) => isHeadless ? console.clear(code) : console.log("exit:", code);
window.openIframe = (src) => {
  return new Promise((resolve, reject) => {
    const iframe = document.createElement("iframe");
    const onerror = reject;
    const onload = () => resolve(iframe);
    document.body.appendChild(Object.assign(iframe, {onload, onerror, src}));
  });
};
