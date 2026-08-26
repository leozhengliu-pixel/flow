import styles from './sub-issue-progress-ring.module.css'

export function SubIssueProgressRing({ completed, total }: { completed: number; total: number }) {
  const ratio=total?completed/total:0,circumference=2*Math.PI*7,trackOffset=6.4835,minimumArc=.4835
  const progressOffset=circumference-(minimumArc+Math.min(1,Math.max(0,ratio))*(circumference-trackOffset-minimumArc))
  return <svg aria-hidden="true" className={styles.ring} viewBox="0 0 16 16"><circle className={styles.track} cx="8" cy="8" r="7" strokeDasharray={circumference} strokeDashoffset={trackOffset} transform="rotate(-47.354 8 8)"/><circle className={styles.value} cx="8" cy="8" r="7" strokeDasharray={circumference} strokeDashoffset={progressOffset} transform="rotate(-76 8 8)"/></svg>
}
