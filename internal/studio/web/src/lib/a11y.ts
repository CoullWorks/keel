import type { KeyboardEvent } from 'react'

// clickable returns the props that make a non-<button> element behave like a
// button for keyboard and screen-reader users: it is focusable (tabIndex 0),
// announced as a button (role), and activates on Enter/Space exactly as a real
// button does. Use it on a clickable <div>/<span>/<th> where switching to a real
// <button> would fight the existing layout/CSS; prefer a real <button> otherwise.
//
// This is the shared version of the pattern the Build wizard already implements
// inline, so every clickable surface behaves the same for keyboard users.
export function clickable(onClick: () => void) {
  return {
    role: 'button',
    tabIndex: 0,
    onClick,
    onKeyDown: (e: KeyboardEvent) => {
      if (e.key === 'Enter' || e.key === ' ') {
        e.preventDefault()
        onClick()
      }
    },
  }
}
