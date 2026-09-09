export async function playNotificationSound() {
  if (typeof AudioContext === 'undefined') return
  const context = new AudioContext()
  try {
    if (context.state === 'suspended') { await context.close(); return }
    const oscillator = context.createOscillator()
    const gain = context.createGain()
    oscillator.type = 'sine'
    oscillator.frequency.setValueAtTime(880,context.currentTime)
    gain.gain.setValueAtTime(0.035,context.currentTime)
    gain.gain.exponentialRampToValueAtTime(0.001,context.currentTime+0.16)
    oscillator.connect(gain);gain.connect(context.destination)
    oscillator.start();oscillator.stop(context.currentTime+0.18)
    oscillator.onended=()=>void context.close()
  } catch { await context.close().catch(()=>undefined) }
}
