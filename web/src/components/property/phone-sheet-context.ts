import { createContext, useContext } from 'react'

/**
 * What a phone property sheet names in its heading chip when the pickers belong
 * to one record: an issue page passes its identifier and title so every sheet
 * reads "KEY-1 ⋅ Title". Pickers outside such a record keep their own label.
 */
export interface PhoneSheetRecord { key: string; title: string }

const PhoneSheetContext = createContext<PhoneSheetRecord | undefined>(undefined)

export const PhoneSheetRecordProvider = PhoneSheetContext.Provider
export const usePhoneSheetRecord = () => useContext(PhoneSheetContext)
