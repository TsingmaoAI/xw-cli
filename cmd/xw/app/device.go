package app

import (
	"fmt"
	"os"
	"text/tabwriter"
	
	"github.com/spf13/cobra"
	"github.com/tsingmaoai/xw-cli/internal/logger"
)

// NewDeviceCommand creates the device command for hardware detection
//
// The device command provides subcommands for detecting and listing
// AI accelerator hardware on the server system.
//
// Usage:
//
//	xw device list        # List detected AI chips on server
//	xw device supported   # Show supported chip types
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
		Long: `Detect and manage AI accelerator devices on the server.

This command queries the server to discover domestic AI chips installed
on the server machine, checking their capabilities, and viewing supported models.`,
		Example: `  # List detected AI chips on server
  xw device list

  # Show all supported chip models
  xw device supported`,
	}
	
	cmd.AddCommand(
		newDeviceListCommand(globalOpts),
		newDeviceSupportedCommand(globalOpts),
	)
	
	return cmd
}

// newDeviceListCommand creates the 'device list' subcommand
func newDeviceListCommand(globalOpts *GlobalOptions) *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List detected AI chips on server",
		Long:  `Query the server and list all detected AI accelerator chips.`,
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			client := getClient(globalOpts)
			
			devices, err := client.ListDevices()
			if err != nil {
				return fmt.Errorf("failed to list devices: %w", err)
			}
			
			if len(devices) == 0 {
				fmt.Println("No AI chips detected on the server.")
				fmt.Println("\nTo see supported chips, run: xw device supported")
				return nil
			}
			
			w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
			fmt.Fprintln(w, "CHIP KEY\tCHIP\tCHIP#\tPCI ADDRESS\tVENDOR:DEVICE")
			fmt.Fprintln(w, "--------\t----\t-----\t-----------\t-------------")
			
			for _, device := range devices {
				pciID := fmt.Sprintf("%s:%s", device.VendorID, device.DeviceID)
				
				// Format chip index display for multi-chip cards
				// Format: PhysicalDevice:ChipIndex (e.g., "0:0", "0:1", "1:0", "1:1")
				chipInfo := "-"
				if device.ChipsPerDevice > 1 {
					chipInfo = fmt.Sprintf("%d:%d", device.PhysicalDeviceIndex, device.ChipIndex)
				}
				
				fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\n",
					device.DeviceType, device.ModelName, chipInfo, device.BusAddress, pciID)
			}
			
			w.Flush()
			
			// Count chips by device type
			chipTypes := make(map[string]int)
			for _, device := range devices {
				chipTypes[device.DeviceType]++
			}
			
			fmt.Printf("\nTotal: %d AI chip(s) detected", len(devices))
			if len(chipTypes) > 0 {
				fmt.Printf(" (")
				first := true
				for dt, count := range chipTypes {
					if !first {
						fmt.Printf(", ")
					}
					fmt.Printf("%dx %s", count, dt)
					first = false
				}
				fmt.Printf(")")
			}
			fmt.Println()
			
			return nil
		},
	}
}

// newDeviceSupportedCommand creates the 'device supported' subcommand
func newDeviceSupportedCommand(globalOpts *GlobalOptions) *cobra.Command {
	var typeFilter string
	
	cmd := &cobra.Command{
		Use:   "supported",
		Short: "Show supported AI chip models",
		Long: `Display all AI chip models supported by the server configuration.

This command shows which chip models are configured and supported for
running inference workloads.`,
		Example: `  # List all supported chip models
  xw device supported`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			client := getClient(globalOpts)
			
			deviceTypes, err := client.GetSupportedDevices()
			if err != nil {
				return fmt.Errorf("failed to get supported devices: %w", err)
			}
			
			if len(deviceTypes) == 0 {
				logger.Warn("No device types configured on server")
				return nil
			}
			
			fmt.Println("Supported AI chip models:")
			for _, dt := range deviceTypes {
				fmt.Printf("  - %s\n", dt)
			}
			
			fmt.Printf("\nTotal: %d chip model(s) supported\n", len(deviceTypes))
			
			return nil
		},
	}
	
	cmd.Flags().StringVar(&typeFilter, "type", "", "Filter by device type")
	
	return cmd
}

