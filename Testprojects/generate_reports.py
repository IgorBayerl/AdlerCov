#!/usr/bin/env python3
"""
Generate coverage reports from existing data or regenerate everything with --force.
"""
import subprocess
import sys
import pathlib
import argparse
import platform
import os

# Paths
SCRIPT_ROOT = pathlib.Path(__file__).resolve().parent
CSHARP_TEST_PROJECT = SCRIPT_ROOT / "CSharp/Project_DotNetCore/UnitTests/UnitTests.csproj"
CSHARP_COVERAGE_DIR = SCRIPT_ROOT / "CSharp/Reports"
CSHARP_COBERTURA_XML = CSHARP_COVERAGE_DIR / "coverage.cobertura.xml"
GO_PROJECT_DIR = SCRIPT_ROOT / "Go"
GO_COVERAGE_FILE = GO_PROJECT_DIR / "coverage.out"
GO_COBERTURA_XML = GO_PROJECT_DIR / "coverage.cobertura.xml"
GO_TOOL_CMD_DIR = SCRIPT_ROOT.parent / "cmd"

# Binary paths
def get_binary_name():
    """Get the appropriate binary name for the current platform."""
    system = platform.system().lower()
    if system == "windows":
        return "adlercov.exe"
    else:
        return "adlercov"

BINARY_NAME = get_binary_name()
BINARY_PATH = SCRIPT_ROOT.parent / BINARY_NAME

def run_command(cmd, working_dir=None, show_output=False):
    """Run command and exit on failure."""
    try:
        if show_output:
            # Stream output in real-time
            process = subprocess.Popen(
                cmd, 
                cwd=working_dir,
                stdout=subprocess.PIPE,
                stderr=subprocess.STDOUT,
                text=True,
                bufsize=1,
                universal_newlines=True
            )
            
            # Print output line by line as it comes
            for line in process.stdout:
                print(line.rstrip())
            
            process.wait()
            if process.returncode != 0:
                print(f"Command failed with return code {process.returncode}")
                sys.exit(1)
        else:
            # Capture output
            result = subprocess.run(cmd, cwd=working_dir, check=True, 
                                  capture_output=True, text=True)
        return None
    except FileNotFoundError:
        print(f"Command not found: {cmd[0]}")
        sys.exit(1)

def ensure_dir(path):
    """Create directory if it doesn't exist."""
    path.mkdir(parents=True, exist_ok=True)

def check_existing_data():
    """Check if coverage data already exists."""
    csharp_exists = CSHARP_COBERTURA_XML.exists() and CSHARP_COBERTURA_XML.stat().st_size > 0
    go_exists = GO_COVERAGE_FILE.exists() and GO_COVERAGE_FILE.stat().st_size > 0
    
    return csharp_exists, go_exists

def build_adlercov_binary():
    """Build the AdlerCov binary for better performance."""
    print("Building AdlerCov binary...")
    
    # Remove existing binary if it exists
    if BINARY_PATH.exists():
        BINARY_PATH.unlink()
    
    # Build command based on platform
    build_cmd = ["go", "build", "-o", str(BINARY_PATH), "./main.go"]
    
    try:
        run_command(build_cmd, working_dir=GO_TOOL_CMD_DIR)
        print(f"Successfully built {BINARY_NAME}")
    except Exception as e:
        print(f"Failed to build AdlerCov binary: {e}")
        sys.exit(1)
    
    # Verify the binary was created and is executable
    if not BINARY_PATH.exists():
        print(f"Binary {BINARY_PATH} was not created")
        sys.exit(1)
    
    # Make executable on Unix-like systems
    if platform.system() != "Windows":
        os.chmod(BINARY_PATH, 0o755)

def generate_csharp_coverage():
    """Generate C# coverage data."""
    print("Generating C# coverage...")
    ensure_dir(CSHARP_COVERAGE_DIR)
    
    cmd = [
        "dotnet", "test", str(CSHARP_TEST_PROJECT),
        "--configuration", "Release",
        "/p:CollectCoverage=true", 
        "/p:CoverletOutputFormat=cobertura",
        f"/p:CoverletOutput={CSHARP_COBERTURA_XML.resolve()}"
    ]
    run_command(cmd)
    
    if not CSHARP_COBERTURA_XML.exists():
        print("Failed to generate C# coverage file")
        sys.exit(1)

def generate_go_coverage():
    """Generate Go coverage data."""
    print("Generating Go coverage...")
    
    # Clean old files
    GO_COVERAGE_FILE.unlink(missing_ok=True)
    GO_COBERTURA_XML.unlink(missing_ok=True)
    
    # Generate native coverage
    cmd = ["go", "test", f"-coverprofile={GO_COVERAGE_FILE.name}", "./..."]
    run_command(cmd, working_dir=GO_PROJECT_DIR)
    
    if not GO_COVERAGE_FILE.exists():
        print("Failed to generate Go coverage file")
        sys.exit(1)
    
    # Convert to Cobertura XML
    cmd_str = f'gocover-cobertura < "{GO_COVERAGE_FILE.name}" > "{GO_COBERTURA_XML.name}"'
    subprocess.run(cmd_str, cwd=GO_PROJECT_DIR, shell=True, check=True)

def generate_report(name, report_file, output_dir, report_types, source_dirs=None):
    """Generate a single report using the pre-built AdlerCov binary."""
    # Handle merged report case (report_file is actually the combined input string)
    if name == "merged":
        # For merged reports, report_file is actually the combined input string
        report_input = str(report_file)
    else:
        # For individual reports, check if file exists
        if not report_file.exists():
            print(f"{name} coverage file not found, skipping {name} report")
            return
        report_input = str(report_file.resolve())
    
    print(f"Generating {name} report...")
    ensure_dir(output_dir)
    
    cmd = [
        str(BINARY_PATH),
        f"--report={report_input}",
        f"--output={output_dir.resolve()}",
        "--verbose",
        f"--reporttypes={report_types}"
    ]
    
    if source_dirs:
        cmd.append(f"--sourcedirs={source_dirs.resolve()}")
    
    run_command(cmd, show_output=True)

def run_report_tool(report_types="Html,TextSummary,Lcov"):
    """Run the main report generation tool using the pre-built binary."""
    print("Running report tool...")
    
    if not BINARY_PATH.exists():
        print(f"AdlerCov binary not found at {BINARY_PATH}")
        print("Building binary first...")
        build_adlercov_binary()
    
    # Reports output directories
    reports_base = SCRIPT_ROOT.parent / "reports"
    csharp_reports = reports_base / "cobertura_csharp_report"
    go_reports = reports_base / "gocover_reports" 
    merged_reports = reports_base / "merged_all_reports"
    
    # Generate individual reports
    generate_report("C#", CSHARP_COBERTURA_XML, csharp_reports, report_types)
    generate_report("Go", GO_COVERAGE_FILE, go_reports, report_types, GO_PROJECT_DIR)
    
    # Generate merged report (if both coverage files exist)
    if CSHARP_COBERTURA_XML.exists() and GO_COVERAGE_FILE.exists():
        # Create a temporary "merged input" representation
        merged_input = f"{CSHARP_COBERTURA_XML.resolve()};{GO_COVERAGE_FILE.resolve()}"
        generate_report("merged", pathlib.Path(merged_input), merged_reports, report_types, GO_PROJECT_DIR)
    else:
        print("Missing coverage files, skipping merged report")

def clean_binary():
    """Remove the built binary."""
    if BINARY_PATH.exists():
        print(f"Removing binary {BINARY_PATH}")
        BINARY_PATH.unlink()

def main():
    """Main function with force flag support."""
    parser = argparse.ArgumentParser(description="Generate coverage reports")
    parser.add_argument("--force", action="store_true", 
                       help="Force regeneration of all coverage data")
    parser.add_argument("--report-types", default="Html,TextSummary,Lcov",
                       help="Comma-separated list of report types")
    parser.add_argument("--rebuild-binary", action="store_true",
                       help="Force rebuild of the AdlerCov binary")
    parser.add_argument("--clean", action="store_true",
                       help="Clean up the built binary after execution")
    
    args = parser.parse_args()
    
    # Build or rebuild the binary if needed
    if args.rebuild_binary or not BINARY_PATH.exists():
        build_adlercov_binary()
    
    try:
        if args.force:
            print("Force mode: regenerating all coverage data")
            generate_csharp_coverage()
            generate_go_coverage()
        else:
            # Check existing data
            csharp_exists, go_exists = check_existing_data()
            
            if not csharp_exists and not go_exists:
                print("No existing coverage data found, generating all...")
                generate_csharp_coverage()
                generate_go_coverage()
            elif not csharp_exists:
                print("Missing C# coverage, generating...")
                generate_csharp_coverage()
            elif not go_exists:
                print("Missing Go coverage, generating...")
                generate_go_coverage()
            else:
                print("Using existing coverage data")
        
        run_report_tool(args.report_types)
        print("Report generation complete")
        
    finally:
        # Clean up binary if requested
        if args.clean:
            clean_binary()

if __name__ == "__main__":
    main()