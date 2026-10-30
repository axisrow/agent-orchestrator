// SessionView is lazy so it stays off the startup path. The session route
// renders it and preloads it when idle; the route awaits it so a first open
// never commits a blank pane.
export const loadSessionView = () => import("../components/SessionView");
