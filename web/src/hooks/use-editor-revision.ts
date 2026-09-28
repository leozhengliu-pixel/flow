import { useCallback, useState } from 'react'

/** Lightweight content revision counter for hash-nav / rehydrate dependents (LS-0729). */
export function useEditorRevision(initialRevision = 1) {
  const [revision, setRevision] = useState(initialRevision)
  const onContentChange = useCallback(() => {
    setRevision((current) => current + 1)
  }, [])
  return { revision, setRevision, onContentChange }
}
