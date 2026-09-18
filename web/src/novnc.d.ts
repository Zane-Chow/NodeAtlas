declare module '@novnc/novnc' {
  type Credentials = { username?: string; password?: string; target?: string }
  type Options = { shared?: boolean; credentials?: Credentials; wsProtocols?: string[] }

  export default class RFB extends EventTarget {
    constructor(target: Element, url: string | WebSocket, options?: Options)
    scaleViewport: boolean
    resizeSession: boolean
    clipViewport: boolean
    focusOnClick: boolean
    disconnect(): void
    sendCredentials(credentials: Credentials): void
  }
}
