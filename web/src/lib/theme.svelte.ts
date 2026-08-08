/**
 * Which theme is showing, and how to change it.
 *
 * The initial value is read off the <html> class rather than from
 * localStorage, because `app.html` has already done that resolution before the
 * first paint - including the fall back to the OS preference when nothing is
 * saved. Reading storage again here would duplicate that logic and could
 * disagree with what is on screen.
 *
 * `dark` is a plain boolean rather than 'light' | 'dark' | 'system' because the
 * design has a two-state toggle. A stored value is an explicit choice; the OS
 * preference only decides the very first visit.
 */
let dark = $state(false);

if (typeof document !== 'undefined') {
  dark = document.documentElement.classList.contains('dark');
}

function apply(next: boolean) {
  dark = next;
  document.documentElement.classList.toggle('dark', next);
  try {
    localStorage.setItem('theme', next ? 'dark' : 'light');
  } catch {
    // Site data blocked. The theme still applies for this page; it just will
    // not survive a reload, which beats throwing out of a click handler.
  }
}

export const theme = {
  get dark() {
    return dark;
  },
  toggle: () => apply(!dark)
};
