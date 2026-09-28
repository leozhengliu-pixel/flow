// @ts-nocheck
import { colorConverter as t_2, clampChannel as r_1 } from "./color-converter";
/**
 * Generative theme token engine (LS-0692 darkThemeRefresh + LS-0708 light seeds).
 * Nested elevated/sidebar/menu/focus factories are present for parity but deferred from product wiring (P2).
 */

function themeInputHash(input: Record<string, unknown>): string {
  const normalized = {
    ...input,
    baseTheme: typeof (input as any).baseTheme?.hash === "string" ? (input as any).baseTheme.hash : (input as any).baseTheme,
  };
  // Stable JSON hash (FNV-1a 32-bit hex) — memoization only; not a security digest.
  const s = JSON.stringify(normalized);
  let h = 0x811c9dc5;
  for (let i = 0; i < s.length; i++) {
    h ^= s.charCodeAt(i);
    h = Math.imul(h, 0x01000193);
  }
  return (h >>> 0).toString(16).padStart(8, "0");
}

const d_1 =
  typeof window !== "undefined" &&
  !!window.matchMedia?.(
    "only screen and (min-device-pixel-ratio: 2), only screen and (min-resolution: 192dpi)",
  ).matches;


function l(e, t) {
  let n = {};
  Object.keys(e).forEach((r) => {
    let i = r;
    n[i] = t(e[i], i);
  });
  return n;
}
function u(e) {
  let t = new Map();
  return (n) => {
    let r = f(n);
    let i = t.get(r);
    if (i) {
      return i;
    }
    let a = e(n, r);
    t.set(r, a);
    return a;
  };
}
function d([n, r, i], t) {
  let [a] = t;
  let s = 0;
  let c = 100;
  let l = c;
  let u = false;
  while (c - s > 1) {
    let e = (s + c) / 2;
    let n = [e, r, i];
    if (t_2.sufficientContrastForText(t, n)) {
      u = true;
      l = e;
      if (a > e) {
        s = e;
      } else {
        c = e;
      }
    } else if (a > e) {
      c = e;
    } else {
      s = e;
    }
  }
  if (u) {
    return [l, r, i];
  }
  let d = [r * 0.75, r * 0.5, r * 0.25, 0];
  for (let e of d) {
    s = n;
    c = 100;
    l = c;
    for (u = false; c - s > 0.1; ) {
      let n = (s + c) / 2;
      let r = [n, e, i];
      if (t_2.sufficientContrastForText(r, t)) {
        u = true;
        l = n;
        c = n;
      } else {
        s = n;
      }
    }
    if (u) {
      return [l, e, i];
    }
  }
}
function f(e) {
  return themeInputHash(e);
}

// ---- generateTheme core ----

const themeApi: any = {};
((e: any) => {
    let t = (e.generateTheme = u(n));
    function n(e, hash) {
      let e_base = e.base;
      let i = e_base[0] > 50;
      let s = i && e_base[0] > 97 && e_base[1] < 8;
      let u = Math.min(e.contrast, 30) + Math.max(e.contrast - 30, 0) * 0.25;
      let f = ((i ? -1 : 1) * u) / 30;
      let p = (e, t) =>
        t_2.adjust(
          e,
          l(t, (e) => e && e * f),
        );
      let m = Math.min(e.contrast, 30) + Math.max(e.contrast - 30, 0) * 0.25;
      let h = ((i ? -0.8 : 1) * m) / 70;
      let g = (e, t) =>
        t_2.adjust(
          e,
          l(t, (e) => e && e * h),
        );
      let _ =
        ((i ? -0.9 : 0.8) * (e.contrast + Math.max(e.contrast - 30, 0) * 0.4)) /
        10;
      let v = (e, t) =>
        t_2.adjust(e, {
          ...t,
          l: t.l && t.l * _,
          c: (t.c ?? 0) * _,
        });
      let y = ((i ? -1 : 1) * (3 + (100 - e.contrast) / 70)) / 4;
      let b = (e, t, n) => {
        let r = t_2.adjust(t_2.getTextColor(e), {
          ...t,
          l: t.l && t.l * y,
        });
        if (n) {
          return t_2.adjustTo(r, n);
        }
        return r;
      };
      let x = Math.max(1, 1 + Math.max(e.contrast - 30, 0) / (i ? 50 : 10));
      let S = (t) => t_2.toCss(e.colorFormat, [0, 0, 0, t * x]);
      let C = (1 + Math.abs(e_base[0] - 50) / 50) / 2;
      let bgBaseHover = p(
        e_base,
        i
          ? {
              l: 3.5,
            }
          : {
              l: 4.25,
              c: 0.5,
            },
      );
      let w = p(
        e_base,
        i
          ? {
              l: 3.5,
            }
          : {
              l: -3.25,
              c: 0,
            },
      );
      let bgSubHover = p(
        w,
        i
          ? {
              l: 5,
            }
          : {
              l: 2.5,
              c: 3,
            },
      );
      let ne = p(
        e_base,
        i
          ? {
              l: 5.5,
            }
          : {
              l: 2,
              c: 0.5,
            },
      );
      let bgShadeHover = p(
        ne,
        i
          ? {
              l: 1.5,
            }
          : {
              l: 1,
              c: 0.5,
            },
      );
      let T = v(
        e_base,
        i
          ? {
              l: 3.5,
              c: 1,
            }
          : {
              l: 4,
              c: 0.5,
            },
      );
      let ie = v(
        e_base,
        i
          ? {
              l: 4.5,
              c: 1,
            }
          : {
              l: 5,
              c: 0.5,
            },
      );
      let ae = d_1
        ? v(
            e_base,
            i
              ? {
                  l: 3,
                  c: 1,
                }
              : {
                  l: 6,
                  c: 0.5,
                },
          )
        : T;
      let E = v(
        e_base,
        i
          ? {
              l: 1,
              c: 1,
            }
          : {
              l: 2,
              c: 0.5,
            },
      );
      let oe = v(
        e_base,
        i
          ? {
              l: 2,
              c: 1,
            }
          : {
              l: 2.75,
              c: 0.5,
            },
      );
      let se = d_1
        ? v(
            e_base,
            i
              ? {
                  l: 3,
                  c: 1,
                }
              : {
                  l: 3.5,
                  c: 0.5,
                },
          )
        : E;
      let D = v(
        e_base,
        i
          ? {
              l: 5,
              c: 1,
            }
          : {
              l: 5,
              c: 0.5,
            },
      );
      let ce = v(
        e_base,
        i
          ? {
              l: 9,
              c: 1,
            }
          : {
              l: 7,
              c: 0.5,
            },
      );
      let le = d_1
        ? v(
            e_base,
            i
              ? {
                  l: 5,
                  c: 1,
                }
              : {
                  l: 10,
                  c: 0.5,
                },
          )
        : D;
      let O = v(
        e_base,
        i
          ? {
              l: 17,
              c: 1,
            }
          : {
              l: 20,
              c: 0.5,
            },
      );
      let ue = v(
        e_base,
        i
          ? {
              l: 21,
              c: 1,
            }
          : {
              l: 24,
              c: 0.5,
            },
      );
      let de = d_1
        ? v(
            e_base,
            i
              ? {
                  l: 17,
                  c: 1,
                }
              : {
                  l: 26,
                  c: 0.5,
                },
          )
        : O;
      let k = (e) => {
        let t = i
          ? ((e_base[0] - e[0]) / e_base[0]) * 1.09
          : (e[0] - e_base[0]) / (100 - e_base[0]);
        return [i ? 0 : 100, 0, 0, r_1(t, 0, 1)];
      };
      let A = t_2.mix(
        e_base,
        e.accent,
        (1 + e_base[1] / 30) * (i ? 0.18 : 0.05),
      );
      let bgSelectedHover = p(
        A,
        i
          ? {
              l: 2,
            }
          : {
              l: 2.5,
              c: 2,
            },
      );
      let j = b(
        e_base,
        {
          l: i ? -10 * C : 10,
        },
        {
          c: 0,
        },
      );
      let M = b(e_base, {
        l: (i ? -20 : -10) * C,
        c: 1,
      });
      let N = b(e_base, {
        l: -40 * C,
        c: 1,
      });
      let P = b(e_base, {
        l: -66 * C,
        c: 1,
      });
      let labelLink = b(
        e_base,
        {
          l: -45 * C,
        },
        {
          h: e.accent[2],
          c: 70,
        },
      );
      let me = 1 + Math.max(e.contrast - 30, 0) / 70;
      let he = (i ? 30 : 15) * me;
      let F = (e) => {
        let t = he * (Math.abs(e[0] - e_base[0]) / 100);
        return t_2.adjust(e, {
          l: e[0] > 100 - t ? -t : t,
        });
      };
      let e_accent = e.accent;
      let I = g(
        e_base,
        i
          ? {
              l: -6,
            }
          : {
              l: 12,
              c: 0.75,
            },
      );
      let controlTertiary = g(
        e_base,
        i
          ? {
              l: -6,
            }
          : {
              l: 12,
              c: 0.5,
            },
      );
      let controlTertiaryHover = g(
        e_base,
        i
          ? {
              l: 9,
            }
          : {
              l: 22,
              c: 0.5,
            },
      );
      let controlTertiarySelected = g(
        e_base,
        i
          ? {
              l: 13,
              c: 0,
            }
          : {
              l: 29,
              c: 1.5,
            },
      );
      let L = [48, 59.31, 288.43];
      let R = e.baseTheme
        ? t_2.fromCss(e.baseTheme.color.focusColor)
        : e.accent;
      let be = R[1] > 50 && (i ? R[0] < 90 : R[0] > 30);
      let h_1 = R[1] < 20 ? L[2] : R[2];
      let focusColor = be
        ? R
        : t_2.adjustTo(
            R,
            i
              ? {
                  l: 70,
                  c: 90,
                  h: h_1,
                }
              : {
                  l: 50,
                  c: 120,
                  h: h_1,
                },
          );
      let z = [80, 70, 267];
      let B = [67.5, 45, 210];
      let V = i ? [68, 64.37, 141.95] : [60, 64.37, 141.95];
      let H = [80, 90, 85];
      let U = [66, 80, 48];
      let W = [58, 73, 29];
      let Ce = d(L, j) || L;
      let we = d(z, j) || z;
      let Te = d(B, j) || B;
      let Ee = d(V, j) || V;
      let De = d(H, j) || H;
      let Oe = d(U, j) || U;
      let ke = d(W, j) || W;
      let G = i ? 0.2 : 0.03;
      let K = {
        bgSub: w,
        bgSubHover,
        bgBase: e_base,
        bgBaseHover,
        bgShade: ne,
        bgShadeHover,
        bgSelected: A,
        bgSelectedHover,
        bgFocus: p(
          e_base,
          i
            ? {
                l: 5,
              }
            : {
                l: 9,
                c: 0.5,
              },
        ),
        bgBorder: T,
        bgBorderHover: ie,
        bgBorderThin: ae,
        bgBorderFaint: E,
        bgBorderFaintHover: oe,
        bgBorderFaintThin: se,
        bgBorderSolid: D,
        bgBorderSolidHover: ce,
        bgBorderSolidThin: le,
        bgBorderStrong: O,
        bgBorderStrongHover: ue,
        bgBorderStrongThin: de,
        bgBorderAlpha: k(T),
        bgBorderAlphaHover: k(ie),
        bgBorderAlphaThin: k(ae),
        bgBorderFaintAlpha: k(E),
        bgBorderFaintAlphaHover: k(oe),
        bgBorderFaintAlphaThin: k(se),
        bgBorderSolidAlpha: k(D),
        bgBorderSolidAlphaHover: k(ce),
        bgBorderSolidAlphaThin: k(le),
        bgBorderStrongAlpha: k(O),
        bgBorderStrongAlphaHover: k(ue),
        bgBorderStrongAlphaThin: k(de),
        bgSelectedBorder: v(A, {
          l: 3.5,
          c: 1,
        }),
        bgSelectedBorderHover: v(A, {
          l: 4.5,
          c: 1,
        }),
        labelBase: M,
        labelBaseHover: F(M),
        labelFaint: P,
        labelLink,
        labelMuted: N,
        labelMutedHover: F(N),
        labelTitle: j,
        labelTitleHover: F(j),
        bgModalOverlay: [0, 0, 0, r_1((i ? 0.25 : 0.4) * x, 0, 0.8)],
        controlPrimary: e_accent,
        controlPrimaryHover: p(e_accent, {
          l: i ? 6 : 5,
          c: 2,
        }),
        controlPrimaryLabel: b(
          e.accent,
          {},
          {
            c: Math.min(5, e.accent[1]),
          },
        ),
        controlSecondary: I,
        controlSecondaryHover: g(I, {
          l: 12,
          c: 1,
        }),
        controlSecondarySelected: g(
          I,
          i
            ? {
                l: 15,
                c: 1,
              }
            : {
                l: 22,
                c: 1,
              },
        ),
        controlSecondaryLabel: M,
        controlTertiary,
        controlTertiaryHover,
        controlTertiaryLabel: M,
        controlTertiarySelected,
        scrollbarBg: t_2.adjust(P, {
          l: 0.3,
        }),
        scrollbarBgHover: t_2.adjust(
          P,
          i
            ? {
                l: 0.8,
              }
            : {
                l: 0.4,
              },
        ),
        scrollbarBgActive: P,
        chromeTabBg: p(e_base, {
          l: i ? -2.5 : 5,
          c: i ? 0 : 2,
        }),
        chromeTabBgHover: p(e_base, {
          l: i ? -5 : 7,
          c: i ? 0 : 2,
        }),
        chromeTabBgActive: p(e_base, {
          l: i ? -8 : 10,
          c: i ? 0 : 2,
        }),
        blueBase: z,
        blueBaseHover: p(z, {
          l: 5,
        }),
        blueBg: we,
        blueMid: p(z, {
          l: 8,
        }),
        blueText: b(
          z,
          {},
          {
            l: i ? 50 : 80,
            c: 80,
          },
        ),
        blueForeground: t_2.getTextColor(we),
        blueTint: t_2.mix(e_base, z, G),
        greenBase: V,
        greenBaseHover: p(V, {
          l: 5,
        }),
        greenBg: Ee,
        greenMid: p(V, {
          l: 8,
        }),
        greenText: b(
          V,
          {},
          {
            l: i ? 50 : 80,
            c: 80,
          },
        ),
        greenForeground: t_2.getTextColor(Ee),
        greenTint: t_2.mix(e_base, V, G),
        orangeBase: U,
        orangeBaseHover: p(U, {
          l: 5,
        }),
        orangeBg: Oe,
        orangeMid: p(U, {
          l: 8,
        }),
        orangeText: b(
          U,
          {},
          {
            l: i ? 50 : 80,
            c: 80,
          },
        ),
        orangeForeground: t_2.getTextColor(Oe),
        orangeTint: t_2.mix(e_base, U, G),
        purpleBase: L,
        purpleBaseHover: p(L, {
          l: 5,
        }),
        purpleBg: Ce,
        purpleMid: p(L, {
          l: 8,
        }),
        purpleText: b(
          L,
          {},
          {
            l: i ? 50 : 80,
            c: 80,
          },
        ),
        purpleForeground: t_2.getTextColor(Ce),
        purpleTint: t_2.mix(e_base, L, G),
        redBase: W,
        redBaseHover: p(W, {
          l: 5,
        }),
        redBg: ke,
        redMid: p(W, {
          l: 8,
        }),
        redText: b(
          W,
          {},
          {
            l: i ? 50 : 80,
            c: 80,
          },
        ),
        redForeground: t_2.getTextColor(ke),
        redTint: t_2.mix(e_base, W, G),
        tealBase: B,
        tealBaseHover: p(B, {
          l: 5,
        }),
        tealBg: Te,
        tealMid: p(B, {
          l: 8,
        }),
        tealText: b(
          B,
          {},
          {
            l: i ? 50 : 80,
            c: 80,
          },
        ),
        tealForeground: t_2.getTextColor(Te),
        tealTint: t_2.mix(e_base, B, G),
        yellowBase: H,
        yellowBaseHover: p(H, {
          l: 5,
        }),
        yellowBg: De,
        yellowMid: p(H, {
          l: 8,
        }),
        yellowText: b(
          H,
          {},
          {
            l: i ? 50 : 80,
            c: 80,
          },
        ),
        yellowForeground: t_2.getTextColor(De),
        yellowTint: t_2.mix(e_base, H, G),
        scrollBackground: i ? [100, 0, 0, 0] : [0, 0, 0, 0.004],
        shadowColor: v(i ? [0, 0, 0, 0.03] : [0, 0, 0, 0.15], {
          l: 1,
        }),
        focusColor,
        githubLogo: N,
        sidebarLinkBg: p(
          e_base,
          i
            ? {
                l: 2.8,
              }
            : {
                l: 6.5,
                c: 1,
              },
        ),
        sidebarLinkBgActive: p(
          e_base,
          i
            ? {
                l: 4.9,
              }
            : {
                l: 12.5,
                c: 1,
              },
        ),
      };
      let q = l(K, (t) => t_2.toCss(e.colorFormat, t));
      let Ae;
      let je;
      let J;
      let Me;
      let Ne;
      let Y;
      let Pe = S(0.02);
      let X = S(0.04);
      let Fe = S(0.06);
      let Ie = S(0.07);
      let Le = S(0.08);
      let Z = S(0.1);
      let Q = S(0.125);
      let Re = S(0.3);
      let ze = (e) => {
        if (e === q.bgBase) {
          return e_base;
        }
        if (t_2.ANY_COLOR_REGEX.test(e)) {
          return t_2.fromCss(e);
        }
        return e_base;
      };
      let $ = {
        hash,
        shadowColor: q.shadowColor,
        contrast: e.contrast,
        colorFormat: e.colorFormat,
        focusShadow: `0 0 0 1px ${q.focusColor}`,
        shadowLow: i
          ? `0px 3px 6px -2px ${Pe}, 0px 1px 1px ${X}`
          : `0px 0.5px 1px 1px ${Re}`,
        shadowBorder: `0 0 0 0.5px ` + q.bgBorder,
        shadowMedium: i
          ? `0 6px 18px ${Pe}, 0 3px 9px ${X}, 0 1px 1px ${X}`
          : `0 3px 8px ${Q}, 0 2px 5px ${Q}, 0 1px 1px ${Q}`,
        shadowHigh: i
          ? `0 9px 48px ${Le}, 0 6px 24px ${Z},  0 1px 1px ${X}`
          : `0 4px 40px ${Z}, 0 3px 20px ${Q},0 3px 12px ${Q}, 0 2px 8px ${Q}, 0 1px 1px ${Q}`,
        shadowInset: `0 1px 1px inset ${Ie}, 0 1px 3px inset ${Ie}, 0 2px 5px inset ${Z}`,
        inputPadding: `6px 12px`,
        inputPaddingBlock: `6px`,
        inputPaddingInline: `12px`,
        inputBackground: q.bgBase,
        inputBorder: `1px solid ${q.bgBorder}`,
        inputBorderRadius: `8px`,
        inputFontSize: `0.8125rem`,
        color: q,
        isDark: !i,
        highlightVariant: (t) => {
          let n = t_2.fromCss(t);
          let r = s
            ? n[1] > 2
              ? {
                  l: -5,
                  c: 6,
                }
              : {
                  l: -8,
                }
            : {
                l: 8,
                c: 5,
              };
          return t_2.toCss(e.colorFormat, t_2.adjust(n, r));
        },
        textHighlight(t, n) {
          return t_2.toCss(
            e.colorFormat,
            t_2.mix(
              ze(this.color.bgBase),
              i
                ? t_2.adjust(t_2.fromCss(t), {
                    l: 7,
                    c: 8,
                  })
                : t_2.fromCss(t),
              n,
            ),
          );
        },
        elevatedTheme: () => {
          Ae ||=
            e.elevation === -1 && e.baseTheme
              ? e.baseTheme
              : t({
                  ...e,
                  elevation: (e.elevation ?? 0) + 1,
                  baseTheme: $,
                  _themeType: `elevated`,
                  base: p(K.bgBase, {
                    l: i ? -8 : 4.125,
                    c: i && !s ? 0 : 0.5,
                  }),
                });
          return Ae;
        },
        subTheme: () => {
          je ||=
            !s && e.elevation === 1 && e.baseTheme
              ? e.baseTheme
              : {
                  ...t({
                    ...e,
                    elevation: (e.elevation ?? 0) - 1,
                    base: w,
                    baseTheme: $,
                    _themeType: `sub`,
                  }),
                };
          return je;
        },
        sidebarTheme: () => {
          if (!J) {
            J = e.sidebarInput
              ? t({
                  ...e,
                  ...e.sidebarInput,
                  baseTheme: $,
                  _themeType: `sidebar`,
                })
              : $.subTheme();
            let n = e.sidebarInput ? J.elevatedTheme() : $;
            let r = e.sidebarInput ? e.sidebarInput.base[0] > 50 : i;
            let a = e.sidebarInput
              ? r && e.sidebarInput.base[0] > 97 && e.sidebarInput.base[1] < 8
              : s;
            J.color.controlTertiaryHover = r ? Fe : n.color.controlSecondary;
            J.color.controlSecondary = a
              ? n.color.bgBase
              : r
                ? n.color.bgShade
                : n.color.controlSecondary;
            J.color.controlSecondaryHover = a
              ? n.color.bgBaseHover
              : r
                ? n.color.bgShadeHover
                : n.color.controlSecondaryHover;
          }
          return J;
        },
        menuTheme: () => {
          Me ||=
            ($.baseTheme !== undefined &&
              ![`base`, `elevated`].includes(e._themeType ?? ``) &&
              $.baseTheme?.menuTheme()) ||
            t({
              ...e,
              baseTheme: $,
              _themeType: `menu`,
              base: p(K.bgBase, {
                l: i ? -8 : 8,
                c: i && !s ? 0 : 0.5,
              }),
            });
          return Me;
        },
        selectedTheme: () => {
          Y ||= t({
            ...e,
            base: K.bgSelected,
            baseTheme: $,
            _themeType: `selected`,
          });
          return Y;
        },
        focusTheme: () => {
          Ne ||= t({
            ...e,
            base: K.bgFocus,
            baseTheme: $,
            _themeType: `focus`,
          });
          return Ne;
        },
        baseTheme: e.baseTheme,
      };
      return $;
    }
  })(themeApi);

export const DARK_THEME_BASE: [number, number, number] = [5.52, 0.4, 272];
export const DARK_THEME_ACCENT: [number, number, number] = [
  47.917542332560124, 59.30267706856808, 288.42138382943733,
];
export const DARK_THEME_CONTRAST = 27;

export const LIGHT_THEME_BASE: [number, number, number] = [97.94, 0.5, 282];
export const LIGHT_THEME_ACCENT: [number, number, number] = [53, 52.26, 286.91];
export const LIGHT_THEME_CONTRAST = 30;

export const LIGHT_HIGH_CONTRAST_BASE: [number, number, number] = [
  98.7, 0.5, 282.86346318829925,
];
export const HIGH_CONTRAST = 90;

export function generateTheme(input: {
  base: [number, number, number] | [number, number, number, number];
  accent: [number, number, number] | [number, number, number, number];
  colorFormat?: string;
  contrast: number;
  baseTheme?: any;
  sidebarInput?: boolean;
}) {
  return themeApi.generateTheme({
    colorFormat: "LCH",
    ...input,
  });
}

/** Default dark theme (LS-0692 seed). */
export function darkThemeRefresh(colorFormat = "LCH") {
  return generateTheme({
    base: DARK_THEME_BASE,
    accent: DARK_THEME_ACCENT,
    colorFormat,
    contrast: DARK_THEME_CONTRAST,
  });
}

/** Dark high-contrast variant. */
export function darkHighContrastTheme(colorFormat = "LCH") {
  return generateTheme({
    base: [8, 0.75, 272],
    accent: DARK_THEME_ACCENT,
    colorFormat,
    contrast: HIGH_CONTRAST,
  });
}

/** Default light theme (LS-0708 seed). */
export function lightThemeRefresh(colorFormat = "LCH") {
  return generateTheme({
    base: LIGHT_THEME_BASE,
    accent: LIGHT_THEME_ACCENT,
    colorFormat,
    contrast: LIGHT_THEME_CONTRAST,
  });
}

/** Light high-contrast variant (LS-0708). */
export function lightHighContrastTheme(colorFormat = "LCH") {
  return generateTheme({
    base: LIGHT_HIGH_CONTRAST_BASE,
    accent: LIGHT_THEME_ACCENT,
    colorFormat,
    contrast: HIGH_CONTRAST,
  });
}
