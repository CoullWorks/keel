import { Component, type ErrorInfo, type ReactNode } from 'react'

// ErrorBoundary keeps one broken subtree from taking down the whole studio. React
// unmounts the entire root when an error thrown during render/lifecycle reaches it
// with no boundary in between — so without this, a single buggy or malicious
// plugin component (or any thrown error in one surface) blanks the console and
// forces a reload. Wrapping each plugin mount and each top-level surface means a
// failure is contained to that box: the shell, nav and console stay alive.
//
// It must be a class component — React exposes error boundaries only through
// getDerivedStateFromError / componentDidCatch, which have no hook equivalent.
type Props = {
  children: ReactNode
  // label names what failed, for the fallback message (e.g. a plugin or surface).
  label?: string
  // resetKey, when it changes, clears the error — so navigating away from a broken
  // surface and back retries it instead of showing the failure forever.
  resetKey?: unknown
}

type State = { error: Error | null }

export class ErrorBoundary extends Component<Props, State> {
  state: State = { error: null }

  static getDerivedStateFromError(error: Error): State {
    return { error }
  }

  componentDidUpdate(prev: Props) {
    // A new resetKey (e.g. a route change) means "try this subtree again".
    if (prev.resetKey !== this.props.resetKey && this.state.error) {
      this.setState({ error: null })
    }
  }

  componentDidCatch(error: Error, info: ErrorInfo) {
    // Surface it in the console for debugging; the studio has no telemetry.
    console.error('studio caught a render error' + (this.props.label ? ' in ' + this.props.label : ''), error, info)
  }

  render() {
    if (this.state.error) {
      return (
        <div className="err">
          {this.props.label ? this.props.label + ' failed to render' : 'This section failed to render'} —{' '}
          {String(this.state.error.message || this.state.error)}
        </div>
      )
    }
    return this.props.children
  }
}
