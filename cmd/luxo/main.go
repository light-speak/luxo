// Command luxo is the CLI for the Luxo language toolchain.
//
// Usage:
//
//	luxo init <project>     Create a new Luxo project
//	luxo add <module>       Add a service module
//	luxo gen                Generate Go code from .luxo schemas
//	luxo run                Start the development server
//	luxo deploy <target>    Generate deployment files
package main

import (
	"os"
)

func main() {
	if err := rootCmd.Execute(); err != nil {
		os.Exit(1)
	}
}
