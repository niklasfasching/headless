export class Connection {
  id = 0
  commands = {}
  handlers = {}

  constructor(url) {
    this.ws = new WebSocket(url);
    this.ws.onmessage = ({data}) => {
      const json = JSON.parse(data)
      if (this.commands[json.id]) this.commands[json.id](json);
      else this.handlers["on"+json.method]?.(json.params);
    };
    this.ws.onerror = (err) => { throw err };
    this.ready = new Promise((resolve) => { this.ws.onopen = resolve });
  }

  emit(method, params) {
    this.ws.send(JSON.stringify({method, params}));
  }

  call(method, params, noreply) {
    const id = this.id++, err = new Error();
    return new Promise((resolve, reject) => {
      this.commands[id] = ({result, error}) => {
        delete this.commands[id];
        if (error) reject(Object.assign(err, {message: `${method}(${JSON.stringify(params)}): ${error.message} (${error.code})`}));
        else resolve(result);
      };
      this.ws.send(JSON.stringify({id, method, params}));
    });
  }

  on(method, f) {
    this.handlers["on"+method] = f;
  }
}

export class Browser extends Connection {
  targets = []

  constructor(url) {
    super(url);
    window.onunload = () => {
      for (const t of this.targets) this.browser.call("Target.closeTarget", t);
    };
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

export class Page extends Connection {
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
}

export class Headless {
  constructor(url) {
    this.server = new Connection(url);
    this.server.handlers = this;
  }

  async onconnect({browserWebsocketUrl}) {
    this.browser = new Browser(browserWebsocketUrl);
    await this.browser.ready;
    const {targetInfos} = await this.browser.call("Target.getTargets");
    const {targetId} = targetInfos.find(t => t.url === location.href);
    this.targetId = targetId;
    const url = `${new URL(this.browser.ws.url).origin}/devtools/page/${targetId}`;
    this.page = new Page(url);
    await this.page.ready;
    await this.page.call("Runtime.enable");
    this.page.on("Runtime.consoleAPICalled", ({type: method, args}) => {
      this.server.emit(method, {url: location.href, args: args.map(formatConsoleArg)});
    });
    this.page.on("Runtime.exceptionThrown", ({exceptionDetails}) => {
      this.server.emit("exception", {url: location.href, args: [formatExceptionDetails(exceptionDetails)]});
    });
    document.body.append(document.head.querySelector("template").content);
    this.server.emit("connect", {url});
  }

  async onopen({url}) {
    await this.browser.ready;
    await this.browser.call("Target.createTarget", {url});
  }

  async onclose() {
    this.server.emit("close", {url: location.href});
    await this.browser.call("Target.closeTarget", {targetId: this.targetId});
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
