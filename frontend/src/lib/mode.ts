// isWails is true when running inside the Wails WebView (desktop).
// In browser/Docker mode it is false.
export const isWails: boolean = typeof (window as any).runtime !== 'undefined'
