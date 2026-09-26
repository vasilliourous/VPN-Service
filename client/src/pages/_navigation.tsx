import HomeOutlinedIcon from '@mui/icons-material/HomeOutlined'
import SettingsOutlinedIcon from '@mui/icons-material/SettingsOutlined'
import { type ComponentType, type ReactNode } from 'react'

import HomeSvg from '@/assets/image/itemicon/home.svg?react'
import SettingsSvg from '@/assets/image/itemicon/settings.svg?react'

import { navigationItems } from './_navigation-meta'
import AccountPage from './account'
import ConnectionPage from './connection'

type NavigationItem = {
  label: (typeof navigationItems)[keyof typeof navigationItems]['label']
  path: string
  icon: ReactNode[]
  Component: ComponentType
}

/**
 * The navigation, reduced to the two things a student does:
 * turn the VPN on, and look at their account.
 *
 * The removed entries are not hidden — they are gone. `proxies`, `logs` and the
 * Verge settings screens were deleted with their components; see the commit
 * that removed them for the shared pieces that were kept deliberately.
 */
export const navItems: NavigationItem[] = [
  {
    ...navigationItems.connection,
    icon: [<HomeOutlinedIcon key="mui" />, <HomeSvg key="svg" />],
    Component: ConnectionPage,
  },
  {
    ...navigationItems.account,
    icon: [<SettingsOutlinedIcon key="mui" />, <SettingsSvg key="svg" />],
    Component: AccountPage,
  },
]
