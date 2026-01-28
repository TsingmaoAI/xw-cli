package app

import (
	"fmt"
	"os"
	"text/tabwriter"
	
	"github.com/spf13/cobra"
	"github.com/tsingmao/xw/internal/device"
	"github.com/tsingmao/xw/internal/logger"
)

// NewDeviceCommand creates the device command for hardware detection
//
// The device command provides subcommands for detecting and listing
// AI accelerator hardware on the system.
//
// Usage:
//
//	xw device list        # List detected AI chips
//	xw device scan        # Scan PCI devices
//	xw device supported   # Show supported chips
//
// Parameters:
//   - globalOpts: Global options shared across commands
//
// Returns:
//   - A configured cobra.Command for device operations
func NewDeviceCommand(globalOpts *GlobalOptions) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "device",
		Short: "AI chip device detection and management",
		Long: `Detect and manage AI accelerator devices.

This command provides tools for discovering domestic AI chips installed
on the system, checking their capabilities, and viewing supported models.`,
		Example: `  # List detected AI chips
  xw device list

  # Scan all PCI devices
  xw device scan

  # Show all supported chip models
  xw device supported`,
	}
	
	cmd.AddCommand(
		newDeviceListCommand(globalOpts),
		newDeviceScanCommand(globalOpts),
		newDeviceSupportedCommand(globalOpts),
	)
	
	return cmd
}

// newDeviceListCommand creates the 'device list' subcommand
func newDeviceListCommand(globalOpts *GlobalOptions) *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List detected AI chips",
		Long:  `Scan the system and list all detected AI accelerator chips.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			chips, err := device.FindAIChips()
			if err != nil {
				return fmt.Errorf("failed to detect AI chips: %w", err)
			}
			
			if len(chips) == 0 {
				fmt.Println("No AI chips detected on this system.")
				fmt.Println("\nTo see supported chips, run: xw device supported")
				return nil
			}
			
			w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
			fmt.Fprintln(w, "DEVICE TYPE\tCHIP\tPCI ADDRESS\tVENDOR:DEVICE")
			fmt.Fprintln(w, "-----------\t----\t-----------\t-------------")
			
			for deviceType, deviceChips := range chips {
				for _, chip := range deviceChips {
					pciID := fmt.Sprintf("%s:%s", chip.VendorID, chip.DeviceID)
					fmt.Fprintf(w, "%s\t%s\t%s\t%s\n",
						deviceType, chip.ModelName, chip.BusAddress, pciID)
				}
			}
			
			w.Flush()
			
			// Show summary
			totalChips := 0
			for _, deviceChips := range chips {
				totalChips += len(deviceChips)
			}
			fmt.Printf("\nTotal: %d AI chip(s) detected\n", totalChips)
			
			return nil
		},
	}
}

// newDeviceScanCommand creates the 'device scan' subcommand
func newDeviceScanCommand(globalOpts *GlobalOptions) *cobra.Command {
	var showAll bool
	
	cmd := &cobra.Command{
		Use:   "scan",
		Short: "Scan PCI devices",
		Long:  `Scan all PCI devices on the system and optionally show detailed information.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			devices, err := device.ScanPCIDevices()
			if err != nil {
				return fmt.Errorf("failed to scan PCI devices: %w", err)
			}
			
			if globalOpts.Verbose {
				logger.Info("Found %d PCI devices", len(devices))
			}
			
			w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
			fmt.Fprintln(w, "PCI ADDRESS\tVENDOR:DEVICE\tCHIP")
			fmt.Fprintln(w, "-----------\t-------------\t----")
			
			aiChipCount := 0
			for _, dev := range devices {
				chip := device.GetChipByID(dev.VendorID, dev.DeviceID)
				
				if !showAll && chip == nil {
					continue
				}
				
				pciID := fmt.Sprintf("%s:%s", dev.VendorID, dev.DeviceID)
				chipInfo := "-"
				if chip != nil {
					chipInfo = fmt.Sprintf("%s (%s)", chip.ModelName, chip.DeviceType)
					aiChipCount++
				}
				
				fmt.Fprintf(w, "%s\t%s\t%s\n", dev.BusAddress, pciID, chipInfo)
			}
			
			w.Flush()
			
			if showAll {
				fmt.Printf("\nTotal: %d PCI device(s), %d AI chip(s)\n", len(devices), aiChipCount)
			} else {
				fmt.Printf("\nTotal: %d AI chip(s) found\n", aiChipCount)
				fmt.Println("\nTo see all PCI devices, use: xw device scan --all")
			}
			
			return nil
		},
	}
	
	cmd.Flags().BoolVarP(&showAll, "all", "a", false,
		"show all PCI devices, not just AI chips")
	
	return cmd
}

// newDeviceSupportedCommand creates the 'device supported' subcommand
func newDeviceSupportedCommand(globalOpts *GlobalOptions) *cobra.Command {
	var deviceType string
	
	cmd := &cobra.Command{
		Use:   "supported",
		Short: "Show supported chip models",
		Long:  `Display all chip models supported by xw with their PCI IDs.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
			
			if deviceType == "" {
				// Show all vendors and their chips
				fmt.Fprintln(w, "VENDOR\tDEVICE TYPE\tCHIP\tPCI ID")
				fmt.Fprintln(w, "------\t-----------\t----\t------")
				
				for _, vendor := range device.KnownVendors {
					chips := device.GetChipsByVendor(vendor.VendorID)
					for _, chip := range chips {
						pciID := fmt.Sprintf("%s:%s", chip.VendorID, chip.DeviceID)
						fmt.Fprintf(w, "%s\t%s\t%s\t%s\n",
							vendor.VendorName, chip.DeviceType, chip.ModelName, pciID)
					}
				}
			} else {
				// Show chips for specific device type
				fmt.Printf("Supported chips for %s:\n\n", deviceType)
				fmt.Fprintln(w, "CHIP\tGENERATION\tPCI ID\tCAPABILITIES")
				fmt.Fprintln(w, "----\t----------\t------\t------------")
				
				// Map device type string to api.DeviceType
				for _, chip := range device.KnownChips {
					if string(chip.DeviceType) == deviceType {
						pciID := fmt.Sprintf("%s:%s", chip.VendorID, chip.DeviceID)
						capabilities := fmt.Sprintf("%v", chip.Capabilities)
						fmt.Fprintf(w, "%s\t%s\t%s\t%s\n",
							chip.ModelName, chip.Generation, pciID, capabilities)
					}
				}
			}
			
			w.Flush()
			
			// Show summary
			fmt.Printf("\nTotal: %d chip(s) from %d vendor(s)\n",
				len(device.KnownChips), len(device.KnownVendors))
			
			return nil
		},
	}
	
	cmd.Flags().StringVarP(&deviceType, "type", "t", "",
		"filter by device type (kunlun, ascend, hygon, longxin)")
	
	return cmd
}

