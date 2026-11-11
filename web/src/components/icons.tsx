/**
 * The app's icon set (Phosphor). Pages import icons from here, never from an icon package, so the
 * whole product shares one look and the set can be swapped in one place.
 *
 * Weight comes from <IconContext> (regular by default; navigation and empty states use duotone).
 * For call-site convenience, `fill="currentColor"` selects the filled weight and a heavy
 * `strokeWidth` selects bold.
 */
import type { Icon as PhosphorIcon, IconWeight } from '@phosphor-icons/react'
import {
  ArchiveIcon, ArrowClockwiseIcon, ArrowCounterClockwiseIcon, ArrowDownIcon, ArrowLeftIcon, ArrowRightIcon, ArrowSquareOutIcon, ArrowUpIcon, ArrowUpRightIcon,
  ArrowsLeftRightIcon, ArrowsMergeIcon, ArrowsOutSimpleIcon, BellSimpleIcon, BookOpenTextIcon, BookOpenUserIcon, BrainIcon, BuildingsIcon, CalendarBlankIcon, CalendarDotsIcon,
  CaretDownIcon, CaretLeftIcon, CaretRightIcon, CaretUpDownIcon, CaretUpIcon, ChatCenteredTextIcon, ChatCircleIcon, CheckCircleIcon, CheckIcon, CircleIcon, CircleNotchIcon,
  ClipboardTextIcon, ClockIcon, CommandIcon, CopyIcon, CrownSimpleIcon, CurrencyCnyIcon, DotsSixVerticalIcon, DotsThreeIcon, DownloadSimpleIcon, EnvelopeSimpleIcon, EyeIcon, EyeSlashIcon,
  FileArrowUpIcon, FileTextIcon, FlagIcon, FlaskIcon, FolderOpenIcon, GaugeIcon, GearSixIcon, GiftIcon, HardDrivesIcon, HeartIcon, IconContext, ImageIcon, ImagesIcon, InfoIcon,
  KanbanIcon, KeyIcon, LayoutIcon, LifebuoyIcon, LightningIcon, LinkSimpleIcon, ListChecksIcon, ListIcon, LockSimpleIcon, LockSimpleOpenIcon, MagicWandIcon, SignOutIcon,
  MagnifyingGlassIcon, MaskHappyIcon, MegaphoneIcon, MicrophoneIcon, MicrophoneStageIcon, MinusIcon, MonitorIcon, MoonIcon, MusicNotesIcon, PackageIcon,
  PaperPlaneTiltIcon, PathIcon, PauseIcon, PencilSimpleIcon, PlayIcon, PlaylistIcon, PlusIcon, ProhibitIcon, QuestionIcon, ReceiptIcon, RobotIcon, ScissorsIcon, ScrollIcon,
  SealCheckIcon, ShapesIcon, ShieldCheckIcon, ShieldWarningIcon, SidebarSimpleIcon, SlidersHorizontalIcon, SparkleIcon, SquareIcon, StackSimpleIcon, StorefrontIcon, SunIcon,
  TagIcon, TicketIcon, TranslateIcon, TrashIcon, UniteIcon, UploadSimpleIcon, UserCircleIcon, UserIcon, UserPlusIcon, UsersThreeIcon, WalletIcon, WarningIcon,
  WaveformIcon, XCircleIcon, XIcon,
} from '@phosphor-icons/react'
import { forwardRef } from 'react'

export { IconContext }
export type { IconWeight }

export type IconProps = Omit<React.SVGProps<SVGSVGElement>, 'ref'> & { size?: number | string; weight?: IconWeight; mirrored?: boolean; strokeWidth?: number | string }
export type AppIcon = React.ForwardRefExoticComponent<IconProps & React.RefAttributes<SVGSVGElement>>

function icon(C: PhosphorIcon, name: string): AppIcon {
  const I = forwardRef<SVGSVGElement, IconProps>(function I({ fill, strokeWidth, weight, size = 24, ...rest }, ref) {
    const w: IconWeight | undefined = weight ?? (fill && fill !== 'none' ? 'fill' : Number(strokeWidth) >= 2.5 ? 'bold' : undefined)
    return <C ref={ref} size={size} weight={w} aria-hidden {...(rest as any)} />
  })
  I.displayName = name
  return I
}

// Names follow the call sites (and the icons they replaced); the Phosphor glyph is on the right.
export const AlertTriangle = icon(WarningIcon, 'AlertTriangle')
export const Archive = icon(ArchiveIcon, 'Archive')
export const ArrowDown = icon(ArrowDownIcon, 'ArrowDown')
export const ArrowLeft = icon(ArrowLeftIcon, 'ArrowLeft')
export const ArrowLeftRight = icon(ArrowsLeftRightIcon, 'ArrowLeftRight')
export const ArrowRight = icon(ArrowRightIcon, 'ArrowRight')
export const ArrowUp = icon(ArrowUpIcon, 'ArrowUp')
export const ArrowUpRight = icon(ArrowUpRightIcon, 'ArrowUpRight')
export const AudioLines = icon(WaveformIcon, 'AudioLines')
export const BadgeCheck = icon(SealCheckIcon, 'BadgeCheck')
export const Ban = icon(ProhibitIcon, 'Ban')
export const Bell = icon(BellSimpleIcon, 'Bell')
export const BookOpen = icon(BookOpenTextIcon, 'BookOpen')
export const BookPlus = icon(BookOpenUserIcon, 'BookPlus')
export const Bot = icon(RobotIcon, 'Bot')
export const Boxes = icon(ShapesIcon, 'Boxes')
export const BrainCircuit = icon(BrainIcon, 'BrainCircuit')
export const Building2 = icon(BuildingsIcon, 'Building2')
export const Calendar = icon(CalendarBlankIcon, 'Calendar')
export const CalendarClock = icon(CalendarDotsIcon, 'CalendarClock')
export const Check = icon(CheckIcon, 'Check')
export const CheckCircle2 = icon(CheckCircleIcon, 'CheckCircle2')
export const ChevronDown = icon(CaretDownIcon, 'ChevronDown')
export const ChevronLeft = icon(CaretLeftIcon, 'ChevronLeft')
export const ChevronRight = icon(CaretRightIcon, 'ChevronRight')
export const ChevronUp = icon(CaretUpIcon, 'ChevronUp')
export const ChevronsUpDown = icon(CaretUpDownIcon, 'ChevronsUpDown')
export const Circle = icon(CircleIcon, 'Circle')
export const CircleDollarSign = icon(CurrencyCnyIcon, 'CircleDollarSign')
export const CircleHelp = icon(QuestionIcon, 'CircleHelp')
export const ClipboardCheck = icon(ClipboardTextIcon, 'ClipboardCheck')
export const Clock = icon(ClockIcon, 'Clock')
export const Combine = icon(UniteIcon, 'Combine')
export const Command = icon(CommandIcon, 'Command')
export const Copy = icon(CopyIcon, 'Copy')
export const Crown = icon(CrownSimpleIcon, 'Crown')
export const Download = icon(DownloadSimpleIcon, 'Download')
export const Drama = icon(MaskHappyIcon, 'Drama')
export const ExternalLink = icon(ArrowSquareOutIcon, 'ExternalLink')
export const Eye = icon(EyeIcon, 'Eye')
export const EyeOff = icon(EyeSlashIcon, 'EyeOff')
export const FileText = icon(FileTextIcon, 'FileText')
export const FileUp = icon(FileArrowUpIcon, 'FileUp')
export const Flag = icon(FlagIcon, 'Flag')
export const FlaskConical = icon(FlaskIcon, 'FlaskConical')
export const FolderOpen = icon(FolderOpenIcon, 'FolderOpen')
export const Gauge = icon(GaugeIcon, 'Gauge')
export const Gift = icon(GiftIcon, 'Gift')
export const Heart = icon(HeartIcon, 'Heart')
export const HelpCircle = icon(QuestionIcon, 'HelpCircle')
export const Image = icon(ImageIcon, 'Image')
export const ImagePlus = icon(ImagesIcon, 'ImagePlus')
export const Info = icon(InfoIcon, 'Info')
export const KanbanSquare = icon(KanbanIcon, 'KanbanSquare')
export const KeyRound = icon(KeyIcon, 'KeyRound')
export const Languages = icon(TranslateIcon, 'Languages')
export const Layers = icon(StackSimpleIcon, 'Layers')
export const LayoutTemplate = icon(LayoutIcon, 'LayoutTemplate')
export const LifeBuoy = icon(LifebuoyIcon, 'LifeBuoy')
export const Link2 = icon(LinkSimpleIcon, 'Link2')
export const ListChecks = icon(ListChecksIcon, 'ListChecks')
export const ListMusic = icon(PlaylistIcon, 'ListMusic')
export const Loader2 = icon(CircleNotchIcon, 'Loader2')
export const Lock = icon(LockSimpleIcon, 'Lock')
export const LogOut = icon(SignOutIcon, 'LogOut')
export const Mail = icon(EnvelopeSimpleIcon, 'Mail')
export const Maximize2 = icon(ArrowsOutSimpleIcon, 'Maximize2')
export const Megaphone = icon(MegaphoneIcon, 'Megaphone')
export const Menu = icon(ListIcon, 'Menu')
export const Merge = icon(ArrowsMergeIcon, 'Merge')
export const MessageCircle = icon(ChatCircleIcon, 'MessageCircle')
export const MessageSquare = icon(ChatCenteredTextIcon, 'MessageSquare')
export const Mic = icon(MicrophoneIcon, 'Mic')
export const Mic2 = icon(MicrophoneStageIcon, 'Mic2')
export const Minus = icon(MinusIcon, 'Minus')
export const Monitor = icon(MonitorIcon, 'Monitor')
export const Moon = icon(MoonIcon, 'Moon')
export const MoreHorizontal = icon(DotsThreeIcon, 'MoreHorizontal')
export const GripVertical = icon(DotsSixVerticalIcon, 'GripVertical')
export const Music = icon(MusicNotesIcon, 'Music')
export const Package = icon(PackageIcon, 'Package')
export const PanelRight = icon(SidebarSimpleIcon, 'PanelRight')
export const Pause = icon(PauseIcon, 'Pause')
export const Pencil = icon(PencilSimpleIcon, 'Pencil')
export const Play = icon(PlayIcon, 'Play')
export const Plus = icon(PlusIcon, 'Plus')
export const Receipt = icon(ReceiptIcon, 'Receipt')
export const RefreshCw = icon(ArrowClockwiseIcon, 'RefreshCw')
export const RotateCcw = icon(ArrowCounterClockwiseIcon, 'RotateCcw')
export const Route = icon(PathIcon, 'Route')
export const Scissors = icon(ScissorsIcon, 'Scissors')
export const ScrollText = icon(ScrollIcon, 'ScrollText')
export const Search = icon(MagnifyingGlassIcon, 'Search')
export const Send = icon(PaperPlaneTiltIcon, 'Send')
export const ServerCog = icon(HardDrivesIcon, 'ServerCog')
export const Settings = icon(GearSixIcon, 'Settings')
export const ShieldAlert = icon(ShieldWarningIcon, 'ShieldAlert')
export const ShieldCheck = icon(ShieldCheckIcon, 'ShieldCheck')
export const SlidersHorizontal = icon(SlidersHorizontalIcon, 'SlidersHorizontal')
export const Sparkles = icon(SparkleIcon, 'Sparkles')
export const Square = icon(SquareIcon, 'Square')
export const Store = icon(StorefrontIcon, 'Store')
export const Sun = icon(SunIcon, 'Sun')
export const Tag = icon(TagIcon, 'Tag')
export const Ticket = icon(TicketIcon, 'Ticket')
export const Trash2 = icon(TrashIcon, 'Trash2')
export const Unlock = icon(LockSimpleOpenIcon, 'Unlock')
export const Upload = icon(UploadSimpleIcon, 'Upload')
export const User = icon(UserIcon, 'User')
export const UserPlus = icon(UserPlusIcon, 'UserPlus')
export const UserRound = icon(UserCircleIcon, 'UserRound')
export const Users = icon(UsersThreeIcon, 'Users')
export const Wallet = icon(WalletIcon, 'Wallet')
export const Wand2 = icon(MagicWandIcon, 'Wand2')
export const X = icon(XIcon, 'X')
export const XCircle = icon(XCircleIcon, 'XCircle')
export const Zap = icon(LightningIcon, 'Zap')
